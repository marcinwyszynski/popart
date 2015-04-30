package popart

import (
	"io"
	"net"
)

// HandlerFactory is an object capable of creating per-session handlers.
type HandlerFactory interface {
	// GetSessionHandler returns a Handler object for a session with a
	// remote client (peer). Returning a nil Handler is an option if the
	// server does not want to communicate with this particuar client - in
	// this case the session is not created but it is advisable to log the
	// fact to aid in debugging - the Server consciously avoids any sort of
	// logging itself.
	GetSessionHandler(peer net.Addr) Handler
}

// Handler is an object capable of serving a POP3 connection.
type Handler interface {
	// AuthenticatePASS is generally the first method called on a Handler
	// and should authentication fail it will return an error. It is
	// expected though that if the authentication is successful the handler
	// will be able to associate all subsequent operations with this
	// particular user without an explicit need to pass username to each and
	// every method.
	AuthenticatePASS(username, password string) error

	// AuthenticateAPOP provides an alternative method of POP3
	// authentication where instead of a username/password combination the
	// client generates an md5 hexdigest based on a shared secret and the
	// banner displayed by the server at the beginning of the connection.
	// As per RFC1939 a server MUST support at least one authentication
	// mechanism but does not need to support any particular one.
	AuthenticateAPOP(username, hexdigest string) error

	// DeleteMessage takes a list of ordinal number of messages in a user's
	// maildrop and deletes them. If this method fails it is expected that
	// *none* of the messages will be deleted.
	// Note: you can not assume that message IDs will come in any particular
	// order.
	DeleteMessages(numbers []uint64) error

	// GetMessageReader takes an ordinal number of a message in a user's
	// maildrop and returns an io.ReadCloser allowing the content of the
	// message to be read. The server will take care of closing the data
	// source.
	GetMessageReader(number uint64) (io.ReadCloser, error)

	// GetMessageCount returns a number of messages waiting in the user's
	// maildrop.
	GetMessageCount() (uint64, error)

	// GetMessageCount takes and ordinal number of a message in a user's
	// maildrop and returns it's locally (per-maildrop) unique ID that is
	// persistent between sessions.
	GetMessageID(number uint64) (string, error)

	// GetMessageCount takes an ordinal number of a message in a users's
	// maildrop and returns its size in bytes. This may differ from what is
	// eventually returned to the client because of line ending replacements
	// and dot escapes but it should be reasonably close nevertheless.
	GetMessageSize(number uint64) (uint64, error)

	// HandleSessionError would be invoked if the code *outside* of the
	// handler errors produces an error. The session itself will terminate
	// but this is a chance to log an error the way
	HandleSessionError(err error)

	// LockMaildrop puts a global lock on the user's maildrop so that
	// any concurrent sessions that attempt to communicate with the server
	// should fail until the current session calls UnlockMaildrop. This
	// method should return an error if it is not possible to lock the
	// maildrop.
	LockMaildrop() error

	// SetBanner is called by APOP-enabled servers at the beginning of the
	// session. It is expected that the banner is stored somewhere since it
	// is expected that it will be available for proper handling of the
	// AuthenticateAPOP call.
	SetBanner(banner string) error

	// UnlockMaildrop releases global maildrop lock so that other clients
	// can connect and initiate their sessions. It is generally the very
	// last thing that will be called on a connection under normal
	// circumstances.
	UnlockMaildrop() error
}
