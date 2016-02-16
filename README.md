# popart [![GoDoc](https://godoc.org/github.com/slowmail-io/popart?status.svg)](https://godoc.org/github.com/slowmail-io/popart) [![codebeat badge](https://codebeat.co/badges/7c72f4f2-ba1a-453d-85fe-fb3936af3c8a)](https://codebeat.co/projects/github-com-slowmail-io-popart)

POP3 server library for Go

![Artwork](http://blogs.artinfo.com/artintheair/files/2012/10/Newsweek-cover-crop.jpg)

Philosophy
---

This library is designed to only take care of handling POP3 specifics on top of the excellent standard library `net/textproto` package. It makes a few opinionated choices:

* it uses interfaces and dependency injection to allow the user integrate their own logic with the protocol handler, much the same way the stock `net/http` library does. The use of interfaces was thought to encourage better design and stronger guarantees than providing functional callback hooks;
* it does not do any logging of its own and leaves all of that to the user. A hook called `HandleSessionError` is provided in the `Handler` interface for handling non-reportable errors that may happen during a POP3 session in case custom logging was desirable. Thanks to `popart` being completely silent the user is free to choose any logging mechanism they like and have the application behave in a consistent fashion;
* it does not support `STARTTLS`. Since it's optional you can't really decide whether the client will end up using it or not. And if they decide not to use it their email will go throught the interpipes in plaintext. This would be perfectly fine if it did not involve other folks' data. So in order to avoid such mishaps this package is designed to take a `net.Listener` which for any sort of production use should be a TLS socket from the `crypto/tls` standard library package.

Installation
---

```
go get github.com/slowmail-io/popart
```

Example
---

The `example` directory contains an example implementation which takes a path to a directory containing email messages (using `--maildir` flag) and serves them through POP3 protocol. It runs on port 1100 by default (can be changed with `--port` flag) and without SSL so you can connect to it directly using telnet:

```
$ telnet localhost 1100
```

Then you can have a nice POP3 chat. The example implementation will actually *not* delete your messages so that you can play around with the same set of files without having to continuously to feed the directory new mail to delete ;)
