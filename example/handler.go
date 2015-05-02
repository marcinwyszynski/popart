package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/slowmail-io/popart"
)

type ByModTime []os.FileInfo

func (m ByModTime) Len() int      { return len(m) }
func (m ByModTime) Swap(i, j int) { m[i], m[j] = m[j], m[i] }
func (m ByModTime) Less(i, j int) bool {
	return m[i].ModTime().UnixNano() < m[j].ModTime().UnixNano()
}

type maildirHandler struct {
	baseDir  string
	messages []os.FileInfo
	username string
}

func NewMaildirHander(dirname string) (popart.Handler, error) {
	fileInfos, err := ioutil.ReadDir(dirname)
	if err != nil {
		return nil, err
	}
	sort.Sort(ByModTime(fileInfos))
	return &maildirHandler{dirname, fileInfos, ""}, nil
}

func (m *maildirHandler) AuthenticatePASS(username, password string) error {
	log.Printf("Logged in with username %q and password %q", username, password)
	m.username = username
	return nil
}

func (m *maildirHandler) AuthenticateAPOP(username, hexdigest string) error {
	log.Printf("Logged in with username %q and hexdigest %q", username, hexdigest)
	m.username = username
	return nil
}

func (m *maildirHandler) DeleteMessages(numbers []uint64) error {
	strNums := make([]string, len(numbers), len(numbers))
	for i, number := range numbers {
		strNums[i] = fmt.Sprintf("%d", number)
	}
	log.Printf(
		"Following messages would be deleted: %q",
		strings.Join(strNums, ", "),
	)
	return nil
}

func (m *maildirHandler) GetMessageReader(number uint64) (io.ReadCloser, error) {
	return os.Open(filepath.Join(m.baseDir, m.messages[number-1].Name()))
}

func (m *maildirHandler) GetMessageCount() (uint64, error) {
	return uint64(len(m.messages)), nil
}

func (m *maildirHandler) GetMessageID(number uint64) (string, error) {
	return m.messages[number-1].Name(), nil
}

func (m *maildirHandler) GetMessageSize(number uint64) (uint64, error) {
	return uint64(m.messages[number-1].Size()), nil
}

func (m *maildirHandler) HandleSessionError(err error) {
	log.Printf("Session error occurred: %v, expected nil", err)
}

func (m *maildirHandler) LockMaildrop() error {
	log.Printf("Mailbox for user %q would be locked", m.username)
	return nil
}

func (m *maildirHandler) SetBanner(banner string) error {
	log.Printf("Banner would be set to %q", banner)
	return nil
}

func (m *maildirHandler) UnlockMaildrop() error {
	log.Printf("Mailbox for user %q would be unlocked", m.username)
	return nil
}
