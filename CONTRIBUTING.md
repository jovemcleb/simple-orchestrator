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

Antes de abrir um PR, os gates precisam passar (o CI roda os mesmos):

```powershell
gofmt -l .       # não pode listar nada
go vet ./...
go test ./...
# se mexeu no install.ps1:
$e=$null; [void][System.Management.Automation.Language.Parser]::ParseFile("$PWD\install.ps1",[ref]$null,[ref]$e); $e
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
6. **Uma conversa, um processo.** Ao continuar um chat existente, verifique antes se ele está aberto em outro app ou janela, e recuse com uma instrução clara. O Codex trava a conversa (`~/.codex/thread-writer-locks/<id>.lock`), enquanto o `agy` só avisa e deixa as duas instâncias sobrescreverem o histórico.

### Contrato do `WorkerResult`

| Situação | `Status` | `error` | Campos |
| --- | --- | --- | --- |
| Sucesso comprovado | `StatusSucceeded` | `nil` | `Summary` (resposta final, **não vazia**), `WorkerSessionID` real, `ObservedModel` (ou `"unknown"`), `Usage` só se for desta tarefa. |
| Falha (inclui troca de modelo, janela fechada, timeout) | `StatusFailed` | não nulo, com o mesmo motivo | `Summary` com o motivo legível, mais o que se souber de sessão e modelo. |
| Pergunta bloqueante ao usuário | `StatusBlocked` | `nil` | `Question` com a pergunta. |

O motor (`internal/runtime`) trata `error != nil` ou `StatusFailed` como falha. Um sucesso com `Summary` vazio vira falha. Se `WorkerSessionID` vier vazio, o motor cria um ID sintético (`worker-<task>-<attempt>`), e isso impede continuar a conversa depois; preencha sempre que a CLI informar.

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
- **Modo interativo** (`NativeWatch`, padrão): abra a CLI com `codex.DefaultNativeLauncher(dir, argv)`. Ele cria um console real, define `CI=true` e tenta deixar a janela fora do job de quem chamou (com fallback). Se a CLI vier do npm, não passe pelo `.cmd`, que reinterpretaria o prompt: use `codex.ResolveNodeCLI` com o caminho do script de entrada dentro de `node_modules`, que você encontra no próprio `.cmd` do npm. Exemplo: `codex.ResolveNodeCLI(cli, "@github", "copilot", "npm-loader.js")` devolve `[node.exe, ...\node_modules\@github\copilot\npm-loader.js]`; acrescente seus argumentos a esse slice. Depois acompanhe os arquivos de sessão até o fim do turno, filtrando eventos anteriores ao início (sessões retomadas). Um estado "ocioso sem resposta final" pode ser transitório: só declare falha se ele persistir por alguns segundos. Em timeout ou cancelamento, chame `kill()`; se a janela fechar antes do fim, falhe com o motivo.
- Deixe injetáveis o lançador, a sonda, o intervalo de leitura e a checagem de "conversa aberta", para os testes.

### 3. Registre o provedor

Checklist:

- [ ] `internal/protocol`: adicione o `Provider` e inclua-o em `ValidProvider`.
- [ ] `internal/protocol/ResolveModelAndEffort`: crie um `case` com o modelo e o esforço padrão (e apelidos, se houver). Sem isso, o provedor cai no ramo genérico (`"default"`, esforço vazio). Se a CLI exigir valores explícitos, recuse o padrão no adaptador, como o Copilot faz.
- [ ] `cmd/simple-orchestrator/main.go`: registre o worker em `buildEngine`, chame `mon.SetNative(...)` para ele, e adicione em `runDelegate` a flag do caminho da CLI (ex.: `--claude-cli`). Atualize os textos de ajuda que listam provedores aceitos.
- [ ] `internal/monitor/FormatLine`: ensine-o a traduzir a saída sem tela, se ela tiver um formato novo.
- [ ] Um teste de CLI ponta a ponta com worker falso (veja `cmd/simple-orchestrator/main_test.go`).

### 4. Teste

Testes **nunca** abrem janelas nem chamam a CLI real; use lançadores e sondas falsos (veja os `native_test.go`). Cubra no mínimo:

- sucesso, com resposta final, modelo observado e uso;
- um passo com ferramenta **não** encerra a tarefa;
- troca de modelo, erro do turno ou conversa abortada contam como falha;
- janela fechada antes do fim conta como falha;
- timeout encerra a janela;
- turnos antigos de uma sessão retomada são ignorados;
- continuar uma conversa aberta em outro lugar é recusado antes de abrir a janela;
- argumentos com espaços, aspas, `&`, `|` e quebras de linha chegam intactos.

### 5. Ensaio real e documentação

Faça ensaios curtos e baratos (ex.: "Responda apenas TOKEN_OK") e registre em `docs/validation/<provedor>.md`. Matriz mínima:

| Ensaio | Obrigatório |
| --- | --- |
| Codex → novo provedor (chat novo) | Sim |
| Antigravity → novo provedor (chat novo) | Sim |
| Continuar um chat existente do novo provedor | Sim |
| Novo provedor como maestra → Codex ou Antigravity | Só se ele também puder ser maestra |

Depois, atualize `skills/local-agent-flow/SKILL.md`, o `README.md` (requisitos, limitações) e `.specs/project/STATE.md`.

## Skills de desenvolvimento

`.agents/skills/` traz skills de apoio para agentes de código (concorrência e testes em Go). Elas são de terceiros, sob licença MIT; veja `AGENTS.md` e `.agents/skills/sources.json`.
