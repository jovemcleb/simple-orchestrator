# Contribuindo

Obrigado por querer ajudar! Este guia explica como preparar o ambiente, as regras que todo provedor segue e o passo a passo para adicionar uma nova CLI (Claude Code, OpenCode etc.).

## Ambiente

- Windows 10/11, Go 1.27+ e Python 3 no PATH.
- Para ensaios reais: as CLIs envolvidas instaladas e logadas.

```powershell
git clone https://github.com/jovemcleb/simple-orchestrator
cd simple-orchestrator
go test ./...
.\install.ps1   # compila e instala a sua versão local
```

Antes de abrir um PR, os três gates precisam passar (o CI roda os mesmos):

```powershell
gofmt -l .       # não pode listar nada
go vet ./...
go test ./...
```

Use mensagens no formato [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`...).

## Mapa do código

| Caminho | Papel |
| --- | --- |
| `cmd/simple-orchestrator` | CLI (`delegate`, `status`, `ack`, `finish`, `version`) e registro dos provedores em `buildEngine`. |
| `internal/protocol` | Contrato: comandos, eventos, provedores, perfis e `ResolveModelAndEffort`. |
| `internal/store` | Diário e outbox persistidos em `%LOCALAPPDATA%\SimpleOrchestrator`. |
| `internal/runtime` | Motor: executa o worker, grava o evento antes de entregar e controla o ciclo de vida. Define a interface `WorkerRunner`. |
| `internal/monitor` | Log legível por tentativa (`live.log`) e terminal de acompanhamento; `FormatLine` traduz eventos de cada CLI. |
| `internal/providers/codex` | Worker Codex e as peças compartilhadas de janela: `DefaultNativeLauncher` e `ResolveNodeCLI`. |
| `internal/providers/antigravity` | Worker Antigravity (sem tela e interativo). |
| `internal/providers/copilot` | Worker GitHub Copilot (experimental): bom exemplo completo dos dois modos. |
| `skills/local-agent-flow` | Skill que os hosts usam para delegar. |
| `.specs/`, `docs/validation/` | Requisitos (R01–R16), decisões e evidências de ensaios reais. |

## Regras que todo provedor segue

Elas vêm da spec (`.specs/features/local-agent-bridge/spec.md`) e são verificadas nos testes:

1. **Sucesso só com prova (R07).** Código de saída zero, texto parcial ou EOF não bastam. O provedor precisa de um sinal verificável de fim de turno: um evento final no stream, ou um registro no arquivo de sessão da CLI.
2. **Sem troca silenciosa de modelo (R04).** Se a CLI usar outro modelo, ou cair num modo automático, a tarefa falha com o motivo. O modelo observado vem da própria CLI; se ela não informar, use `"unknown"`.
3. **Zero inferência na espera (R06).** Espere pelo processo, por arquivos ou por eventos. Nunca chame modelos para "ver como está".
4. **Uso de tokens honesto (R12).** Só informe `Usage` quando o valor for desta tarefa. Contadores acumulados por sessão não servem para sessões retomadas.
5. **Sessões explícitas.** Retorne o ID real da sessão do worker em `WorkerSessionID`. Nunca use "a última conversa".

## Adicionando um provedor

### 1. Investigue a CLI (spike curto)

Descubra e anote em `docs/validation/<provedor>.md`:
- como passar modelo, esforço, sessão nova ou retomada, e pasta de trabalho;
- se existe saída estruturada (JSONL) no modo sem tela, e qual evento indica o fim do turno;
- onde a CLI grava a sessão (arquivos, SQLite) e como identificar o fim do turno no **modo interativo**, que não termina sozinho;
- se ela troca o modelo sem avisar quando ele está indisponível.

Exemplos dos sinais já usados:

| CLI | Fim do turno no modo interativo |
| --- | --- |
| Codex | `task_complete` no `~/.codex/sessions/**/rollout-*.jsonl` |
| Antigravity | `CASCADE_RUN_STATUS_IDLE` e `not_fully_idle = 0` no `conversation_summaries.db`, mais uma resposta final sem tool calls no `transcript.jsonl` |
| Copilot | `assistant.turn_end` depois de um `assistant.message` sem `toolRequests` no `~/.copilot/session-state/<id>/events.jsonl` |

### 2. Implemente `internal/providers/<provedor>`

Implemente `runtime.WorkerRunner`:

```go
RunTask(ctx context.Context, cmd protocol.CommandEnvelope) (runtime.WorkerResult, error)
```

- Monte o prompt com `codex.BuildPrompt(cmd.Spec)` e resolva modelo e esforço com `protocol.ResolveModelAndEffort`.
- **Modo sem tela:** leia o stream linha a linha, mande cada linha para `monitor.FromContext(ctx).Raw(line)` e decida o resultado só no evento final.
- **Modo interativo** (`NativeWatch`, padrão): abra a CLI com `codex.DefaultNativeLauncher(dir, argv)`, que já resolve console real, `CI=true` e janela fora do job de quem chamou. Se a CLI vier do npm, use `codex.ResolveNodeCLI` para não passar pelo `.cmd`, que reinterpretaria o prompt. Depois acompanhe os arquivos de sessão até o fim do turno, filtrando eventos anteriores ao início (sessões retomadas). Em timeout ou cancelamento, chame `kill()`; se a janela fechar antes do fim, falhe com o motivo.
- Deixe injetáveis o lançador, a sonda e o intervalo de leitura, para os testes.

### 3. Registre o provedor

- Se for um provedor novo, adicione-o em `internal/protocol` (`Provider` e `ValidProvider`).
- Registre o worker em `buildEngine` (`cmd/simple-orchestrator/main.go`) e chame `mon.SetNative(...)` para ele no modo interativo.
- Se a saída sem tela tiver um formato novo, ensine `monitor.FormatLine` a traduzi-lo.

### 4. Teste

Testes **nunca** abrem janelas nem chamam a CLI real; use lançadores e sondas falsos (veja os `native_test.go`). Cubra no mínimo:

- sucesso, com resposta final, modelo observado e uso;
- um passo com ferramenta **não** encerra a tarefa;
- troca de modelo, erro do turno ou conversa abortada contam como falha;
- janela fechada antes do fim conta como falha;
- timeout encerra a janela;
- turnos antigos de uma sessão retomada são ignorados;
- argumentos com espaços, aspas, `&`, `|` e quebras de linha chegam intactos.

### 5. Ensaio real e documentação

- Faça um ensaio curto e barato (ex.: "Responda apenas TOKEN_OK") nas duas direções relevantes e registre em `docs/validation/`.
- Atualize `skills/local-agent-flow/SKILL.md`, o `README.md` e `.specs/project/STATE.md`.

## Skills de desenvolvimento

`.agents/skills/` traz skills de apoio para agentes de código (concorrência e testes em Go). Elas são de terceiros, sob licença MIT; veja `AGENTS.md` e `.agents/skills/sources.json`.
