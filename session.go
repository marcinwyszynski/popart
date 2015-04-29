package popart

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"os"
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
		state:         stateAuthorization,
		username:      "",
		markedDeleted: make(map[uint64]struct{}),
		msgSizes:      make(map[uint64]uint64),
		reader:        textproto.NewReader(bufio.NewReader(conn)),
		writer:        textproto.NewWriter(bufio.NewWriter(conn)),
	}
}

func (s *session) serve() {
	defer s.conn.Close()
	defer s.unlock() // unlock maildrop if locked no matter what
	helloParts := []string{"POP3 server ready"}
	if s.server.APOP {
		banner := s.getBanner()
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
		if s.state == stateTerminateConnection {
			return
		}
		err := s.conn.SetReadDeadline(time.Now().Add(s.server.Timeout))
		if err != nil {
			s.handleError(err)
			return
		}
		line, err := s.reader.ReadLine()
		if err != nil {
			s.handleError(err)
			return // communication problem, most likely?
		}
		args := strings.Split(line, " ")
		command := strings.ToUpper(args[0])
		cmdValidator, exists := validators[command]
		if !exists {
			s.handleError(errInvalidSyntax)
			continue // unknown command
		}
		if err := cmdValidator.validate(s, args[1:]); err != nil {
			s.handleError(err) // these are always reportable
			continue
		}
		action := operationHandlers[command]
		s.handleError(action(s, args[1:]))
	}
}

func (s *session) handleAPOP(args []string) error {
	if !s.server.APOP {
		return ReportableError("server does not support APOP")
	}
	if err := s.handler.AuthenticateAPOP(args[0], args[1]); err != nil {
		return err
	}
	return s.signIn()
}

func (s *session) handleDELE(args []string) error {
	return s.withMessageDo(args[0], func(msgId uint64) {
		s.markedDeleted[msgNo] = struct{}{}
		return s.respondOK("message %d deleted", msgNo)
	})
}

func (s *session) handleLIST(args []string) error {
	if len(args) == 1 {
		return s.withMessageDo(args[0], func(msgId uint64) {
			return s.respondOK("%d %d", msgId, s.msgSizes[msgId])
		})
	}
	return s.forEachMessage(func(msgId uint64) (string, error) {
		return fmt.Sprintf("%d %d", msgId, s.msgSizes[msgId]), nil
	})
}

func (s *session) handleNOOP(args []string) error {
	return s.respondOK("doing nothing")
}

func (s *session) handlePASS(args []string) error {
	if s.username == "" {
		return NewReportableError("please provide username first")
	}
	if err := s.AuthenticatePASS(s.username, args[0]); err != nil {
		return err
	}
	return s.signIn()
}

func (s *session) handleQUIT(args []string) error {
	bye := func() error {
		s.state = stateTerminateConnection
		return s.respondOK(goodbyeMsg)
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

func (s *session) handleRSET(args []string) (err error) {
	s.markedDeleted = make(map[uint64]struct{})
	return s.respondOK(
		"maildrop has %d messages (%d octets)",
		s.getMessageCount(),
		s.getMaildropSize(),
	)
}

func (s *session) handleSTAT(args []string) (err error) {
	return s.respondOK("%d %d", s.getMessageCount(), s.getMaildropSize())
}

func (s *session) handleTOP(args []string) (err error) {
	return s.withMessageDo(args[0], func(msgId uint64) {
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
		for i := 0; i < noLines; i++ {
			line, readErr := protoReader.ReadLine()
			if readErr == io.EOF || readErr == nil {
				if _, err := dotWriter.Write(line); err != nil {
					return err
				}
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return err
			}
			if _, err := dotWriter.Write("\n"); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *session) handleUIDL(args []string) (err error) {
	if len(args) == 1 {
		return s.withMessageDo(args[0], func(msgId uint64) {
			uidl, err := s.handler.GetMessageID(msgId)
			if err != nil {
				return err
			}
			return s.respondOK("%d %s", msgId, uidl)
		})
	}
	return s.forEachMessage(func(msgId uint64) (string, error) {
		return fmt.Sprintf("%d %d", msgId, s.msgSizes[msgId]), nil
	})
}

func (s *session) handleUSER(args []string) (err error) {
	s.username = args[0]
	return s.respondOK("welcome %s", s.username)
}

func (s *session) handleError(err error) {
	if err != nil {
		return
	}
	rErr, isReportable := err.(ReportableError)
	if isReportable {
		if err = s.writer.PrintfLine("-ERR %s", rErr); err == nil {
			return
		}
	}
	s.state = stateTerminateConnection // will terminate the connection!
	s.handler.HandleSessionError(err)
}

func (s *session) respondOK(format string, args ...interface{}) error {
	return s.writer.PrintfLine(fmt.Sprintf("+OK %s", format), args)
}

func (s *session) fetchMaildropStats() error {
	msgCount, err := s.GetMessageCount()
	if err != nil {
		return err
	}
	for i := 0; i < msgCount; i++ {
		mSize, err := s.handler.GetMessageSize(i + 1)
		if err != nil {
			return err
		}
		s.msgSizes[i+1] = mSize
	}
	return nil
}

func (s *session) signIn() error {
	if err := s.LockMaildrop(); err != nil {
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

func (s *session) getMessageCount() uint64 {
	return len(s.msgSizes) - len(s.markedDeleted)
}

func (s *session) getMaildropSize() uint64 {
	var ret uint64
	for msgID, size := range s.msgSizes {
		if _, deleted := s.markedDeleted[msgID]; deleted {
			continue
		}
		ret += size
	}
	return ret
}

func (s *session) forEachMessage(fn func(id uint64) (string, error)) error {
	dotWriter := s.writer.DotWriter()
	defer s.closeOrReport(closer)
	for i := 0; i < len(s.msgSizes); i++ {
		if _, deleted := s.markedDeleted[i+1]; deleted {
			continue
		}
		line, err := fn(i + 1)
		if err != nil {
			return err
		}
		if err := dotWriter.PrintfLine(line); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) withMessageDo(sID string, fn func(id uint64) error) error {
	msgID, err := strconv.ParseUint(sID, 10, 64)
	if err != nil {
		return errInvalidSyntax
	}
	if msgID == 0 || msgID > len(s.msgSizes) {
		return NewReportableError("no such message: %d", msgNo)
	}
	if _, gone := s.markedDeleted[msgNo]; gone {
		return NewReportableError("message %d already deleted", msgId)
	}
	return fn(msgID)
}

func (s *session) unlock() {
	if s.state == stateAuthorization {
		return // we didn't yet even have a chance to lock the maildrop
	}
	if err := s.handler.UnlockMaildrop(); err != nil {
		s.handler.HandleSessionError(err)
	}
}

func (s *session) getBanner() string {
	return fmt.Sprintf(
		"<%d.%d@%s>",
		os.Getpid(),
		time.Time().Unix(),
		s.server.Hostname,
	)
}

func (s *session) closeOrReport(closer io.Closer) {
	if err := closer.Close(); err != nil {
		s.handler.HandleSessionError(err)
	}
}
