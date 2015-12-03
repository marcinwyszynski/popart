package popart

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// Server listens for incoming POP3 connections and handles them with the help
// of Handler objects passed via dependency injection.
type Server struct {
	// Hostname defines how the server should introduce itself. It is only
	// really important if the server is supposed to support APOP
	// authentication method.
	Hostname string

	// OnNewConnection is a callback capable of producing Handler objects
	// to handle incoming connections.
	OnNewConnection func(peer net.Addr) Handler

	// Timeout allows setting an inactivity autologout timer. According to
	// rfc1939 such a timer MUST be of at least 10 minutes' duration.
	Timeout time.Duration

	// Implementation allows the server to provide custom implementation
	// name to the POP3 client. The default one is "popart".
	Implementation string

	// Expire allows the server to provide message expiration advice to the
	// client. The default one is "NEVER".
	Expire string

	// APOP determines whether the server should implement the APOP
	// authentication method.
	APOP bool

	// capabilites is a pre-calculated set of things server can announce to
	// the client upon receiving the CAPA command.
	capabilities []string
}

// Serve takes a net.Listener and starts processing incoming requests. Please
// note that Server does not implement STARTTLS so unless your Listener
// implements TLS (see package crypto/tls in the standard library) all
// communications happen in plaintext. You have been warned.
func (s *Server) Serve(listener net.Listener) error {
	if err := s.verifySettings(); err != nil {
		return err
	}
	s.calculateCapabilities()
	for {
		conn, err := listener.Accept()
		if err != nil && s.handleAcceptError(err) != nil {
			return err
		}
		s.serveOne(conn)
	}
}

func (s *Server) handleAcceptError(err error) error {
	if ne, ok := err.(net.Error); ok && ne.Temporary() {
		time.Sleep(time.Second)
		return nil
	}
	return err
}

func (s *Server) verifySettings() error {
	if s.OnNewConnection == nil {
		return errors.New("new connection callback not be nil")
	}
	if s.Timeout < 10*time.Minute {
		return errors.New("at least 10 minutes timeout required")
	}
	return nil
}

func (s *Server) serveOne(conn net.Conn) {
	handler := s.OnNewConnection(conn.RemoteAddr())
	if handler == nil {
		// This must have been a conscious decision on the
		// part of the HandlerFactory so not treating that as
		// an error. In fact, not even logging it since the
		// OnNewConnection callback is perfectly capable of
		// doing that.
		return
	}
	go newSession(s, handler, conn).serve()
}

func (s *Server) calculateCapabilities() {
	s.Expire = withDefault(s.Expire, "NEVER")
	s.Implementation = withDefault(s.Implementation, "popart")
	s.capabilities = capabilities(s.Expire, s.Implementation)
}

// getBanner is only relevant within the context of an APOP exchange.
func (s *Server) getBanner() string {
	return fmt.Sprintf(
		"<%d.%d@%s>",
		os.Getpid(),
		time.Now().Unix(),
		s.Hostname,
	)
}

func capabilities(expire, implementation string) []string {
	return []string{
		"TOP",
		"USER", // TODO: this should be factored out.
		"PIPELINING",
		fmt.Sprintf("%s %s", "EXPIRE", expire),
		"UIDL",
		fmt.Sprintf("%s %s", "IMPLEMENTATION", implementation),
	}
}

func withDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
