package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/slowmail-io/popart"
)

var (
	maildir = flag.String("maildir", "", "path to the mail directory")
	port    = flag.Int("port", 1100, "TCP port to run POP3 server on")
)

func getHandler(peer net.Addr) popart.Handler {
	log.Printf("Incoming connection from %q", peer)
	handler, err := NewMaildirHander(*maildir)
	if err != nil {
		log.Printf("Error while creating handler: %v, expected nil", err)
	}
	return handler
}

func main() {
	flag.Parse()
	if *maildir == "" {
		log.Fatal("Please provide a location of your mail directory")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("Error while creating listener: %v, expected nil", err)
	}
	server := &popart.Server{
		Hostname:        "slowmail.io",
		OnNewConnection: getHandler,
		Timeout:         10 * time.Minute,
		APOP:            true,
	}
	log.Fatal(server.Serve(listener))
}
