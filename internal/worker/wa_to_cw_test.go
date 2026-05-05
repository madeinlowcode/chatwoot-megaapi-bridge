package worker

import (
	"errors"
	"strings"
	"testing"
)

type retriableErr struct{}

func (retriableErr) Error() string   { return "retriable" }
func (retriableErr) Retriable() bool { return true }

type nonRetriableErr struct{}

func (nonRetriableErr) Error() string   { return "non" }
func (nonRetriableErr) Retriable() bool { return false }

func TestIsRetriable(t *testing.T) {
	if !isRetriable(retriableErr{}) {
		t.Errorf("retriableErr should be retriable")
	}
	if isRetriable(nonRetriableErr{}) {
		t.Errorf("nonRetriableErr should not be retriable")
	}
	if isRetriable(errors.New("plain")) {
		t.Errorf("plain error must not be retriable by default")
	}
	if isRetriable(nil) {
		t.Errorf("nil must not be retriable")
	}
}

func TestJIDToPhone(t *testing.T) {
	cases := []struct{ in, want string }{
		{"5511999999999@s.whatsapp.net", "+5511999999999"},
		{"+5511999@x", "+5511999"},
		{"5511999", "+5511999"},
		{"+5511999", "+5511999"},
		{"@x", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := jidToPhone(c.in); got != c.want {
			t.Errorf("jidToPhone(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestNameOr(t *testing.T) {
	if got := nameOr("", "fb"); got != "fb" {
		t.Errorf("got %q", got)
	}
	if got := nameOr("real", "fb"); got != "real" {
		t.Errorf("got %q", got)
	}
}

func TestClassifyRetriable(t *testing.T) {
	err := classify(retriableErr{}, "k")
	if err == nil || !strings.Contains(err.Error(), "retriable") {
		t.Errorf("got %v", err)
	}
	err2 := classify(nonRetriableErr{}, "k")
	if err2 == nil || !strings.Contains(err2.Error(), "non-retriable") {
		t.Errorf("got %v", err2)
	}
}
