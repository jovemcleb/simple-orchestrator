package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jovemcleb/simple-orchestrator/internal/monitor"
	"github.com/jovemcleb/simple-orchestrator/internal/protocol"
	"github.com/jovemcleb/simple-orchestrator/internal/store"
)

type streamingWorker struct{ result WorkerResult }

func (w *streamingWorker) RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (WorkerResult, error) {
	monitor.FromContext(ctx).Raw([]byte(`{"type":"item.completed","item":{"type":"agent_message","text":"andamento ao vivo"}}`))
	return w.result, nil
}

func TestEngine_Monitor_WritesLiveLogAndOpensTerminal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	st, err := store.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(st, map[protocol.Provider]WorkerRunner{
		protocol.ProviderAntigravity: &streamingWorker{result: WorkerResult{
			Status: protocol.StatusSucceeded, Summary: "ok final", ObservedModel: "m",
			Usage: &protocol.Usage{TotalTokens: 7},
		}},
	})
	opened := make(chan string, 1)
	eng.SetMonitor(monitor.New(st.BaseDir(), func(_, path string) error { opened <- path; return nil }, nil))

	if _, _, err := eng.Delegate(context.Background(), sampleDelegateCmd("run-m", "task-1", "att-1")); err != nil {
		t.Fatal(err)
	}
	path := <-opened
	if !strings.HasPrefix(path, filepath.Join(dir, "live")) {
		t.Errorf("unexpected log path %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"andamento ao vivo", "=== FIM ===", "Resumo: ok final", "total=7"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("live log missing %q:\n%s", want, b)
		}
	}
}

func TestEngine_Monitor_TerminalFailureDoesNotBlockDelegation(t *testing.T) {
	t.Parallel()
	st, _ := store.New(t.TempDir())
	eng := NewEngine(st, map[protocol.Provider]WorkerRunner{
		protocol.ProviderAntigravity: &fakeWorker{result: WorkerResult{Status: protocol.StatusSucceeded, Summary: "ok"}},
	})
	eng.SetMonitor(monitor.New(st.BaseDir(), func(string, string) error { return errors.New("sem wt") }, nil))
	ev, _, err := eng.Delegate(context.Background(), sampleDelegateCmd("run-f", "task-1", "att-1"))
	if err != nil || ev.Status != protocol.StatusSucceeded {
		t.Fatalf("delegation must succeed, ev=%+v err=%v", ev, err)
	}
}
