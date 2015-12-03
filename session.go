package popart

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

const (
	stateAuthorization = iota
	stateTransaction
	stateUpdate
	stateTerminateConnection
)

type operationHandler func(s *session, args []string) error

var (
	operationHandlers = map[string]operationHandler{
		"APOP": (*session).handleAPOP,
		"CAPA": (*session).handleCAPA,
		"DELE": (*session).handleDELE,
		"LIST": (*session).handleLIST,
		"NOOP": (*session).handleNOOP,
		"PASS": (*session).handlePASS,
		"QUIT": (*session).handleQUIT,
		"RETR": (*session).handleRETR,
		"RSET": (*session).handleRSET,
		"STAT": (*session).handleSTAT,
		"TOP":  (*session).handleTOP,
		"UIDL": (*session).handleUIDL,
		"USER": (*session).handleUSER,
	}
)

type session struct {
	server  *Server
	handler Handler
	conn    net.Conn

	state         int
	username      string
	markedDeleted map[uint64]struct{}
	msgSizes      map[uint64]uint64

	reader *textproto.Reader
	writer *textproto.Writer
}

func newSession(server *Server, handler Handler, conn net.Conn) *session {
	return &session{
		server:        server,
		handler:       handler,
		conn:          conn,
		markedDeleted: make(map[uint64]struct{}),
		msgSizes:      make(map[uint64]uint64),
		reader:        textproto.NewReader(bufio.NewReader(conn)),
		writer:        textproto.NewWriter(bufio.NewWriter(conn)),
	}
}

// serve method handles the entire session which after the first message from
// the server is a series of command-response interactions.
func (s *session) serve() {
	defer s.conn.Close()
	defer s.unlock() // unlock maildrop if locked no matter what
	helloParts := []string{"POP3 server ready"}
	if s.server.APOP {
		banner := s.server.getBanner()
		helloParts = append(helloParts, banner)
		if err := s.handler.SetBanner(banner); err != nil {
			s.handler.HandleSessionError(err)
			return // go home handler, you're drunk!
		}
	}
	if err := s.respondOK(strings.Join(helloParts, " ")); err != nil {
		s.handler.HandleSessionError(err)
		return // communication problem, most likely?
	}
	for {
		if keepGoing := s.serveOne(); !keepGoing {
			return
		}
	}
}

// serveOne handles each command-response interaction with the client. The
// boolean return value indicates whether the communication with the client
// should continue or not.
func (s *session) serveOne() bool {
	if s.state == stateTerminateConnection {
		return false
	}
	readBy := time.Now().Add(s.server.Timeout)
	if err := s.conn.SetReadDeadline(readBy); err != nil {
		return s.handleError(err, false)
	}
	line, err := s.reader.ReadLine()
	if err != nil {
		return s.handleError(err, false) // communication problem, most likely?
	}
	args := strings.Split(line, " ")
	command := strings.ToUpper(args[0])
	cmdValidator, exists := validators[command]
	if !exists {
		return s.handleError(errInvalidSyntax, true) // unknown command
	}
	if err := cmdValidator.validate(s, args[1:]); err != nil {
		return s.handleError(err, true)
	}
	return s.handleError(operationHandlers[command](s, args[1:]), true)
}

// handleCAPA is a callback for capability listing.
// RFC 2449, page 2.
func (s *session) handleCAPA(args []string) error {
	if err := s.respondOK("Capability list follows"); err != nil {
		return err
	}
	dotWriter := s.writer.DotWriter()
	defer s.closeOrReport(dotWriter)
	for _, capability := range s.server.capabilities {
		if _, err := fmt.Fprintln(dotWriter, capability); err != nil {
			return err
		}
	}
	return nil
}

// handleAPOP is a callback for an APOP authentication mechanism.
// RFC 1939, page 15.
func (s *session) handleAPOP(args []string) error {
	if !s.server.APOP {
		return NewReportableError("server does not support APOP")
	}
	if err := s.handler.AuthenticateAPOP(args[0], args[1]); err != nil {
		return err
	}
	return s.signIn()
}

// handleDELE is a callback for a single message deletion.
// RFC 1939, page 8.
func (s *session) handleDELE(args []string) error {
	return s.withMessageDo(args[0], func(msgId uint64) error {
		s.markedDeleted[msgId] = struct{}{}
		return s.respondOK("message %d deleted", msgId)
	})
}

// handleAPOP is a callback for listing one or more messages.
// RFC 1939, page 6.
func (s *session) handleLIST(args []string) error {
	if len(args) == 1 {
		return s.withMessageDo(args[0], func(msgId uint64) error {
			return s.respondOK("%d %d", msgId, s.msgSizes[msgId])
		})
	}
	return s.forEachMessage(func(msgId uint64) (string, error) {
		return fmt.Sprintf("%d %d", msgId, s.msgSizes[msgId]), nil
	})
}

// handleNOOP is a callback for a no-op (timeout reset) command.
// RFC 1939, page 9.
func (s *session) handleNOOP(args []string) error {
	return s.respondOK("doing nothing")
}

// handlePASS is a callback for the client providing password ("PASS" command).
// This must have been preceded by a "USER" command where the client provides
// its username.
// RFC 1939, page 14.
func (s *session) handlePASS(args []string) error {
	if s.username == "" {
		return NewReportableError("please provide username first")
	}
	if err := s.handler.AuthenticatePASS(s.username, args[0]); err != nil {
		return err
	}
	return s.signIn()
}

// handleQUIT is a callback for the client terminating the session. It will do
// slightly different things depending on the current state of the transaction.
// RFC 1939, pages 5 (in authorization state) and 10 (in transaction state).
func (s *session) handleQUIT(args []string) error {
	bye := func() error {
		s.state = stateTerminateConnection
		return s.respondOK("dewey POP3 server signing off")
	}
	if s.state == stateAuthorization {
		return bye()
	}
	s.state = stateUpdate // so that no future calls will succeed
	var delMsg []uint64
	for key := range s.markedDeleted {
		delMsg = append(delMsg, key)
	}
	if err := s.handler.DeleteMessages(delMsg); err != nil {
		return err
	}
	return bye()
}

// handleRETR is a callback for the client requesting full content of a a single
// message.
// RFC 1939, page 8.
func (s *session) handleRETR(args []string) (err error) {
	return s.withMessageDo(args[0], func(msgId uint64) error {
		if err := s.respondOK("%d octets", s.msgSizes[msgId]); err != nil {
			return err
		}
		readCloser, err := s.handler.GetMessageReader(msgId)
		if err != nil {
			return err
		}
		defer s.closeOrReport(readCloser)
		dotWriter := s.writer.DotWriter()
		defer s.closeOrReport(dotWriter)
		_, err = io.Copy(dotWriter, readCloser)
		return err
	})
}

// handleRSET is a callback for the client requesting the session to be reset.
// This essentially means undeleting all messages previously marked for
// deletion.
// RFC 1939, page 9.
func (s *session) handleRSET(args []string) error {
	s.markedDeleted = make(map[uint64]struct{})
	return s.respondOK(
		"maildrop has %d messages (%d octets)",
		s.getMessageCount(),
		s.getMaildropSize(),
	)
}

// handleRETR is a callback for the client requesting full content of a a single
// message.
// RFC 1939, page 8.
func (s *session) handleSTAT(args []string) error {
	return s.respondOK("%d %d", s.getMessageCount(), s.getMaildropSize())
}

// handleTOP is a callback for the client requesting a number of lines from the
// top of a single message.
// RFC 1939, page 11.
func (s *session) handleTOP(args []string) error {
	return s.withMessageDo(args[0], func(msgId uint64) error {
		noLines, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return errInvalidSyntax
		}
		if err := s.writer.PrintfLine("+OK"); err != nil {
			return err
		}
		readCloser, err := s.handler.GetMessageReader(msgId)
		if err != nil {
			return err
		}
		defer s.closeOrReport(readCloser)
		dotWriter := s.writer.DotWriter()
		defer s.closeOrReport(dotWriter)
		protoReader := textproto.NewReader(bufio.NewReader(readCloser))
		for i := uint64(0); i < noLines; i++ {
			line, readErr := protoReader.ReadLineBytes()
			if err := printTopLine(line, readErr, dotWriter); err != nil {
				return err
			}
		}
		return nil
	})
}

func printTopLine(line []byte, readErr error, writer io.Writer) error {
	if readErr == io.EOF || readErr == nil {
		if err := writeWithError(writer, line); err != nil {
			return err
		}
	}
	if readErr != nil {
		return readErr
	}
	return writeWithError(writer, []byte{'\n'})
}

func writeWithError(w io.Writer, content []byte) error {
	_, err := w.Write(content)
	return err
}

// handleUIDL is a callback for the client unique message identifiers for
// either one or all messages.
// RFC 1939, page 12.
func (s *session) handleUIDL(args []string) (err error) {
	if len(args) == 1 {
		return s.withMessageDo(args[0], func(msgId uint64) error {
			uidl, err := s.handler.GetMessageID(msgId)
			if err != nil {
				return err
			}
			return s.respondOK("%d %s", msgId, uidl)
		})
	}
	return s.forEachMessage(func(msgId uint64) (string, error) {
		uidl, err := s.handler.GetMessageID(msgId)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%d %s", msgId, uidl), nil
	})
}

// handleUSER is a callback for the client providing it's username. This must be
// followed by a "PASS" command with a corresponding password.
// RFC 1939, page 13.
func (s *session) handleUSER(args []string) (err error) {
	s.username = args[0]
	return s.respondOK("welcome %s", s.username)
}

// handleError provides a helper to decide what to do with the result of a
// single command handler. There are three possible outcomes. First - the
// command succeeded. Second, the command failed but the failure is reported to
// the user and the transaction continues. Third, an error occurred that calls
// for and immediate termination of the session.
func (s *session) handleError(err error, shouldContinue bool) bool {
	if err == nil {
		return shouldContinue
	}
	rErr, isReportable := err.(*ReportableError)
	if isReportable {
		if err = s.writer.PrintfLine("-ERR %s", rErr); err == nil {
			return shouldContinue
		}
	}
	s.state = stateTerminateConnection // will terminate the connection!
	s.handler.HandleSessionError(err)
	return shouldContinue
}

// respondOK provides a helper to write a "success" line to the client, with
// printf-like formatting. It will only fail if it is impossible to write to the
// client (e.g. closed TCP socket).
func (s *session) respondOK(format string, args ...interface{}) error {
	return s.writer.PrintfLine(fmt.Sprintf("+OK %s", format), args...)
}

// fetchMaildropStats queries the handler for message count and sizes and builds
// based on that builds maildrop statistics that are then cached internally
// throughout the whole length of the session.
func (s *session) fetchMaildropStats() error {
	msgCount, err := s.handler.GetMessageCount()
	if err != nil {
		return err
	}
	for i := uint64(0); i < msgCount; i++ {
		mSize, err := s.handler.GetMessageSize(i + 1)
		if err != nil {
			return err
		}
		s.msgSizes[i+1] = mSize
	}
	return nil
}

// signIn is called after successful authentication whereby the protocol
// requires that the maildrop is not available to any other users trying to
// access it concurrently (RFC 1939, page 3).
func (s *session) signIn() error {
	if err := s.handler.LockMaildrop(); err != nil {
		return err
	}
	s.state = stateTransaction
	if err := s.fetchMaildropStats(); err != nil {
		return err
	}
	return s.respondOK(
		"%s's maildrop has %d messages (%d octets)",
		s.username,
		s.getMessageCount(),
		s.getMaildropSize(),
	)
}

// getMessageCount reports the relevant number based on cached data.
func (s *session) getMessageCount() uint64 {
	return uint64(len(s.msgSizes) - len(s.markedDeleted))
}

// getMaildropSize reports the relevant number based on cached data.
func (s *session) getMaildropSize() uint64 {
	var ret uint64
	for msgID, size := range s.msgSizes {
		if _, deleted := s.markedDeleted[msgID]; !deleted {
			ret += size
		}
	}
	return ret
}

// forEachMessage is a helper that allows a callback to be invoked for every
// message in the maildrop that is not deleted. The callback is expected to
// return a line that is then printed out to the client.
func (s *session) forEachMessage(fn func(id uint64) (string, error)) error {
	dotWriter := s.writer.DotWriter()
	defer s.closeOrReport(dotWriter)
	for i := uint64(0); i < uint64(len(s.msgSizes)); i++ {
		if _, deleted := s.markedDeleted[i+1]; deleted {
			continue
		}
		line, err := fn(i + 1)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(dotWriter, line); err != nil {
			return err
		}
	}
	return nil
}

// withMessageDo is a wrapper for handlers operating on a single message. It
// generally makes sure that the message number provided makes sense within
// the context of the current transaction.
func (s *session) withMessageDo(sID string, fn func(id uint64) error) error {
	msgID, err := strconv.ParseUint(sID, 10, 64)
	if err != nil {
		return errInvalidSyntax
	}
	if msgID == 0 || msgID > uint64(len(s.msgSizes)) {
		return NewReportableError("no such message: %d", msgID)
	}
	if _, gone := s.markedDeleted[msgID]; gone {
		return NewReportableError("message %d already deleted", msgID)
	}
	return fn(msgID)
}

// unlock will unlock the client's maildrop if it is locked. Note that we assume
// the mailbox is locked if the exchange proceeded past the authorization stage.
func (s *session) unlock() {
	if s.state == stateAuthorization {
		return // we didn't yet even have a chance to lock the maildrop
	}
	if err := s.handler.UnlockMaildrop(); err != nil {
		s.handler.HandleSessionError(err)
	}
}

// closer provides a wrapper that allows deferred 'Close' operations to have
// their errors reported to the session error handler.
func (s *session) closeOrReport(closer io.Closer) {
	if err := closer.Close(); err != nil {
		s.handler.HandleSessionError(err)
	}
}
