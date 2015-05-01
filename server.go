package popart

import (
	"errors"
	"net"
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

	// APOP determines whether the server should implement the APOP
	// authentication method.
	APOP bool
}

// Serve takes a net.Listener and starts processing incoming requests. Please
// note that Server does not implement STARTTLS so unless your Listener
// implements TLS (see package crypto/tls in the standard library) all
// communications happen in plaintext. You have been warned.
func (s *Server) Serve(listener net.Listener) error {
	if s.OnNewConnection == nil {
		return errors.New("new connection callback not be nil")
	}
	if s.Timeout < 10*time.Minute {
		return errors.New("at least 10 minutes timeout required")
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				time.Sleep(time.Second)
				continue
			}
			return err
		}
		handler := s.OnNewConnection(conn.RemoteAddr())
		if handler == nil {
			// This must have been a conscious decision on the
			// part of the HandlerFactory so not treating that as
			// an error. In fact, not even logging it since the
			// OnNewConnection callback is perfectly capable of
			// doing that.
			continue
		}
		sn := newSession(s, handler, conn)
		go sn.serve()
	}
}
