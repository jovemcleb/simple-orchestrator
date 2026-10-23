package monitor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatLine_Codex(t *testing.T) {
	cases := map[string]string{
		`{"type":"thread.started","thread_id":"abc"}`:                                               "Sessão iniciada: abc",
		`{"type":"item.completed","item":{"type":"agent_message","text":"MYO_OK"}}`:                 "Agente: MYO_OK",
		`{"type":"item.started","item":{"type":"command_execution","command":"go test"}}`:           "Comando: go test",
		`{"type":"item.completed","item":{"type":"command_execution","command":"x","exit_code":1}}`: "Comando concluído (saída 1): x",
		`{"type":"turn.failed","error":{"message":"quota exceeded"}}`:                               "FALHA: quota exceeded",
		`{"type":"turn.completed","usage":{}}`:                                                      "Turno concluído",
		`{"type":"item.started","item":{"type":"agent_message","text":""}}`:                         "",
	}
	for in, want := range cases {
		if got := FormatLine([]byte(in)); got != want {
			t.Errorf("FormatLine(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatLine_AGYAndFallback(t *testing.T) {
	if got := FormatLine([]byte(`{"event":"init","conversation_id":"c1"}`)); got != "Sessão iniciada: c1" {
		t.Errorf("init: %q", got)
	}
	if got := FormatLine([]byte(`{"event":"result","result":{"status":"SUCCESS"}}`)); got != "Resultado: SUCCESS" {
		t.Errorf("result: %q", got)
	}
	if got := FormatLine([]byte(`{"event":"assistant","message":"olá"}`)); got != "assistant: olá" {
		t.Errorf("generic: %q", got)
	}
	if got := FormatLine([]byte(`{"foo":1}`)); got != "" {
		t.Errorf("unknown shape must be skipped, got %q", got)
	}
	if got := FormatLine([]byte("plain text")); got != "plain text" {
		t.Errorf("non-json: %q", got)
	}
	long := strings.Repeat("a", 2000)
	if got := FormatLine([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"` + long + `"}}`)); len([]rune(got)) > maxLineRunes+20 {
		t.Errorf("line not truncated: %d runes", len([]rune(got)))
	}
}

func TestLog_AppendFinishAndNilSafety(t *testing.T) {
	var nilLog *Log
	nilLog.Line("x")
	nilLog.Raw([]byte(`{}`))
	nilLog.Finish("failed", "", "", "", nil)

	var opened struct {
		sync.Mutex
		title, path string
	}
	done := make(chan struct{})
	m := New(t.TempDir(), func(title, path string) error {
		opened.Lock()
		opened.title, opened.path = title, path
		opened.Unlock()
		close(done)
		return nil
	}, nil)
	l, err := m.Start("run 1", "task-1", "att-1", "codex", "Minha tarefa")
	if err != nil {
		t.Fatal(err)
	}
	l.Raw([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"pronto"}}`))
	l.Finish("succeeded", "gpt-x", "tudo certo", "total=3", nil)
	l.Line("depois de fechar") // ignorado

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("opener not called")
	}
	opened.Lock()
	defer opened.Unlock()
	if opened.path != l.Path() || !strings.Contains(opened.title, "Minha tarefa") {
		t.Errorf("opener got title=%q path=%q", opened.title, opened.path)
	}
	b, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"Agente: pronto", "=== FIM ===", "Status: succeeded", "Modelo observado: gpt-x", "Uso: total=3", "Resumo: tudo certo", "somente leitura"} {
		if !strings.Contains(s, want) {
			t.Errorf("log missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "depois de fechar") {
		t.Error("line after Finish must be ignored")
	}
}

func TestStart_OpenFailureIsLoggedAndNotFatal(t *testing.T) {
	notified := make(chan error, 1)
	m := New(t.TempDir(), func(string, string) error { return errors.New("sem terminal") }, func(err error) { notified <- err })
	l, err := m.Start("r", "t", "a", "antigravity", "x")
	if err != nil {
		t.Fatalf("Start must not fail when terminal fails: %v", err)
	}
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("failure not reported")
	}
	l.Finish("succeeded", "", "", "", nil)
	b, _ := os.ReadFile(l.Path())
	if !strings.Contains(string(b), "não foi possível abrir o terminal") {
		t.Errorf("failure not recorded in log:\n%s", b)
	}
}

func TestContext(t *testing.T) {
	if FromContext(context.Background()) != nil {
		t.Fatal("expected nil log")
	}
	m := New(t.TempDir(), nil, nil)
	l, _ := m.Start("r", "t", "a", "codex", "x")
	if FromContext(NewContext(context.Background(), l)) != l {
		t.Fatal("log not round-tripped")
	}
	l.Finish("succeeded", "", "", "", nil)
}

func TestTerminalCommand_WindowsTerminalAndFallback(t *testing.T) {
	logPath := `C:\Users\Example User\it's here\x.live.log`
	withWT := terminalCommand(func(string) (string, error) { return `C:\wt.exe`, nil }, "codex: a;b", logPath)
	if withWT.Path != `C:\wt.exe` || withWT.Args[1] != "new-tab" {
		t.Errorf("expected wt new-tab, got %v", withWT.Args)
	}
	without := terminalCommand(func(string) (string, error) { return "", exec.ErrNotFound }, "t", logPath)
	if !strings.Contains(strings.ToLower(without.Args[0]), "cmd") || without.Args[2] != "start" {
		t.Errorf("expected cmd start fallback, got %v", without.Args)
	}
	for _, c := range []*exec.Cmd{withWT, without} {
		last := c.Args[len(c.Args)-1]
		if strings.ContainsAny(last, " ;'\"") {
			t.Errorf("encoded command must be a single safe token: %q", last)
		}
	}
	if !strings.Contains(psScript("t", logPath), "it''s here") {
		t.Error("single quote in path must be escaped")
	}
}
