// Package monitor writes a human-readable live log for each worker attempt and
// opens a read-only terminal that follows it (R16). It never feeds model prompts
// and never owns the worker process.
package monitor

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
)

const maxLineRunes = 600

// Opener opens a terminal following logPath. Failures must not block delegation.
type Opener func(title, logPath string) error

type Monitor struct {
	baseDir string
	open    Opener
	notify  func(error)
	native  map[string]bool
}

// SetNative marks a provider whose worker runs in its own native CLI window, so
// no separate follow-along terminal is opened for it (the log is still written).
func (m *Monitor) SetNative(provider string) {
	if m.native == nil {
		m.native = map[string]bool{}
	}
	m.native[provider] = true
}

// New returns a monitor storing logs under baseDir. A nil opener only writes logs.
// notify, when set, receives terminal-open failures.
func New(baseDir string, open Opener, notify func(error)) *Monitor {
	return &Monitor{baseDir: baseDir, open: open, notify: notify}
}

type Log struct {
	mu     sync.Mutex
	f      *os.File
	path   string
	closed bool
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "x"
	}
	return s
}

// Start creates the live log for one attempt, writes its header and opens the terminal.
func (m *Monitor) Start(runID, taskID, attemptID, provider, title string) (*Log, error) {
	dir := filepath.Join(m.baseDir, "live", safeName(runID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, safeName(taskID)+"-"+safeName(attemptID)+".live.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	l := &Log{f: f, path: path}
	l.Line(fmt.Sprintf("=== %s | run %s | task %s | tentativa %s ===", provider, runID, taskID, attemptID))
	l.Line("Tarefa: " + title)
	if m.native[provider] {
		l.Line("Acompanhamento: janela interativa da CLI (modo --watch-native)")
	} else if m.open != nil {
		go func() {
			if err := m.open(fmt.Sprintf("%s: %s", provider, title), path); err != nil {
				l.Line("[aviso] não foi possível abrir o terminal de acompanhamento: " + err.Error())
				if m.notify != nil {
					m.notify(err)
				}
			}
		}()
	}
	return l, nil
}

func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Line appends a timestamped line. Safe on a nil Log.
func (l *Log) Line(s string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	for _, part := range strings.Split(strings.TrimRight(s, "\r\n"), "\n") {
		fmt.Fprintf(l.f, "%s %s\n", time.Now().Format("15:04:05"), part)
	}
}

// Raw formats one provider JSONL line and appends it when it is meaningful.
func (l *Log) Raw(raw []byte) {
	if l == nil {
		return
	}
	if s := FormatLine(raw); s != "" {
		l.Line(s)
	}
}

// Finish writes the final summary and closes the log. Safe on a nil Log.
func (l *Log) Finish(status, model, summary, usage string, runErr error) {
	if l == nil {
		return
	}
	l.Line("=== FIM ===")
	l.Line("Status: " + status)
	if model != "" {
		l.Line("Modelo observado: " + model)
	}
	if usage != "" {
		l.Line("Uso: " + usage)
	}
	if runErr != nil {
		l.Line("Erro: " + truncate(runErr.Error()))
	}
	if strings.TrimSpace(summary) != "" {
		l.Line("Resumo: " + strings.TrimSpace(summary))
	}
	l.Line("(terminal somente leitura; pode fechar a janela sem afetar a tarefa)")
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	_ = l.f.Close()
}

type ctxKey struct{}

func NewContext(ctx context.Context, l *Log) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

func FromContext(ctx context.Context) *Log {
	l, _ := ctx.Value(ctxKey{}).(*Log)
	return l
}

func truncate(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > maxLineRunes {
		return string(r[:maxLineRunes]) + "…"
	}
	return string(r)
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// FormatLine turns a Codex (`type`) or Antigravity (`event`) JSONL line into one
// readable line. Unknown shapes become a short generic line; empty means skip.
func FormatLine(raw []byte) string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return truncate(string(raw))
	}
	if t := str(m, "type"); t != "" {
		if _, ok := m["data"]; ok || t == "result" {
			return formatCopilot(t, m)
		}
		return formatCodex(t, m)
	}
	if e := str(m, "event"); e != "" {
		return formatAGY(e, m)
	}
	return ""
}

func formatCodex(t string, m map[string]any) string {
	switch t {
	case "thread.started":
		return "Sessão iniciada: " + str(m, "thread_id")
	case "turn.started":
		return "Turno iniciado"
	case "turn.completed":
		return "Turno concluído"
	case "turn.failed":
		if e, ok := m["error"].(map[string]any); ok {
			return "FALHA: " + truncate(str(e, "message"))
		}
		return "FALHA no turno"
	case "error":
		return "ERRO: " + truncate(str(m, "message"))
	}
	item, _ := m["item"].(map[string]any)
	if item == nil {
		return ""
	}
	state := strings.TrimPrefix(t, "item.")
	switch it := str(item, "type"); it {
	case "agent_message":
		if strings.TrimSpace(str(item, "text")) == "" {
			return ""
		}
		return "Agente: " + truncate(str(item, "text"))
	case "reasoning":
		if s := str(item, "text"); s != "" {
			return "Raciocínio: " + truncate(s)
		}
		return ""
	case "command_execution":
		cmd := truncate(str(item, "command"))
		switch state {
		case "started":
			return "Comando: " + cmd
		case "completed":
			if c, ok := item["exit_code"].(float64); ok {
				return fmt.Sprintf("Comando concluído (saída %d): %s", int(c), cmd)
			}
		}
		return ""
	case "file_change":
		if state == "completed" {
			return "Arquivos alterados: " + truncate(fmt.Sprint(item["changes"]))
		}
		return ""
	default:
		if state == "completed" && it != "" {
			return "Item concluído: " + it
		}
		return ""
	}
}

func formatCopilot(t string, m map[string]any) string {
	d, _ := m["data"].(map[string]any)
	switch t {
	case "session.start":
		return fmt.Sprintf("Sessão iniciada: modelo %s, esforço %s", str(d, "selectedModel"), str(d, "reasoningEffort"))
	case "session.model_change":
		return fmt.Sprintf("Modelo: %s → %s", str(d, "previousModel"), str(d, "newModel"))
	case "session.error":
		return "ERRO: " + truncate(str(d, "message"))
	case "assistant.message":
		if s := str(d, "content"); strings.TrimSpace(s) != "" {
			return "Agente: " + truncate(s)
		}
		return ""
	case "tool.execution_start":
		if s := str(d, "toolTitle"); s != "" {
			return "Ferramenta: " + truncate(s)
		}
		return "Ferramenta: " + str(d, "toolName")
	case "tool.execution_complete":
		if ok, _ := d["success"].(bool); !ok {
			return "Ferramenta falhou"
		}
		return ""
	case "result":
		if c, ok := m["exitCode"].(float64); ok {
			return fmt.Sprintf("Resultado: saída %d", int(c))
		}
		return "Resultado recebido"
	}
	return ""
}

func formatAGY(e string, m map[string]any) string {
	switch e {
	case "init":
		return "Sessão iniciada: " + str(m, "conversation_id")
	case "result":
		if r, ok := m["result"].(map[string]any); ok {
			return "Resultado: " + str(r, "status")
		}
		return "Resultado recebido"
	}
	for _, k := range []string{"text", "content", "message", "response"} {
		if s := str(m, k); s != "" {
			return fmt.Sprintf("%s: %s", e, truncate(s))
		}
	}
	for _, v := range m {
		if sub, ok := v.(map[string]any); ok {
			for _, k := range []string{"text", "content", "message", "name", "tool"} {
				if s := str(sub, k); s != "" {
					return fmt.Sprintf("%s: %s", e, truncate(s))
				}
			}
		}
	}
	return e
}

func psScript(title, logPath string) string {
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	return fmt.Sprintf("$Host.UI.RawUI.WindowTitle = %s; Get-Content -LiteralPath %s -Wait -Encoding UTF8", q(title), q(logPath))
}

// encodePS encodes a script for powershell -EncodedCommand (UTF-16LE base64),
// which avoids quoting problems with spaces, quotes and Windows Terminal's ';'.
func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 0, len(u)*2)
	for _, c := range u {
		b = append(b, byte(c), byte(c>>8))
	}
	return base64.StdEncoding.EncodeToString(b)
}

// terminalCommand builds the command that opens the terminal: Windows Terminal
// when available, otherwise a new PowerShell console window.
func terminalCommand(lookPath func(string) (string, error), title, logPath string) *exec.Cmd {
	enc := encodePS(psScript(title, logPath))
	if wt, err := lookPath("wt.exe"); err == nil {
		return exec.Command(wt, "new-tab", "--title", title, "powershell.exe", "-NoLogo", "-NoExit", "-EncodedCommand", enc)
	}
	return exec.Command("cmd.exe", "/c", "start", "", "powershell.exe", "-NoLogo", "-NoExit", "-EncodedCommand", enc)
}

// OpenTerminal is the default Opener. It starts the terminal and does not wait for it.
func OpenTerminal(title, logPath string) error {
	cmd := terminalCommand(exec.LookPath, title, logPath)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
