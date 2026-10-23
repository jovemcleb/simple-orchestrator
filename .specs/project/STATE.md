# Estado — 23/10/2026

## Fase

**T01–T09 concluídas (inclui T08A). Publicação v0.1 (Codex ⇄ Antigravity) em preparação. T11 (Copilot) implementada como experimental; T10 (Claude) e T12 pendentes.**

- T01 passou com `worker_succeeded=true`, resposta `MYO_T01_WORKER_OK` em 5,35 s e restauração da configuração global MCP com SHA-256 conferido pelo script Python. Evidência: `work/spikes/t01-centralized-skip/result.json` e histórico em `docs/spikes/native-return.md`.
- T02–T08 implementadas e revisadas pela sessão `01a15543-4df3-78b3-853e-3dd07a139b28` (`gpt-6-luna`). A lacuna apontada na revisão de T06 (propagação de erro de `restore()` em `RunTask` e lock entre processos `.myo.lock` em `IsolateGlobalMCPConfig`) foi corrigida em `internal/providers/antigravity/antigravity.go` e validada por `TestAdapter_RunTask_PropagatesRestoreError`. `go vet ./...` e `go test ./...` passam em todos os pacotes.
- T08A (23/10): acompanhamento ao vivo; padrão `--watch-native` abre a tela interativa da CLI do worker (Codex e Antigravity).
- T09 (23/10): delegação real `Codex → AGY` e `AGY → Codex` passou, com retorno à sessão maestra, 0 inferências na espera e janela do worker sobrevivendo ao fim do comando da maestra. Relatório e achados (sandbox do Codex exige escalação; job do AGY sem breakaway resolvido via WMI) em `docs/validation/first-pair.md`. Maestras validadas pelas CLIs (`codex exec`, `agy -p`); validação visual nas UIs fica para T12.
- T11 (23/10, experimental): adaptador Copilot CLI em `internal/providers/copilot` (sem tela via `-p --output-format json` e interativo via `-i`, fim do turno por `events.jsonl`, `--session-id` definido pela ponte, modelo e esforço obrigatórios). Ensaio real bloqueado: a CLI desta máquina não está logada numa conta com modelos explícitos (só `auto` funciona) e, no modo interativo, troca o modelo indisponível por `auto` sem parar; a ponte detecta essa troca, encerra a janela e falha (R04). Desligado por padrão (`--experimental-copilot`).
- Publicação (23/10): projeto renomeado para `simple-orchestrator` (módulo `github.com/jovemcleb/simple-orchestrator`), repositório público MIT, instalador `install.ps1` (binário em `%LOCALAPPDATA%\Programs\simple-orchestrator`, skill nos hosts encontrados), CI e release por tag. Estado das execuções em `%LOCALAPPDATA%\SimpleOrchestrator`. A skill `desenha-sistema` não é distribuída (sem licença de redistribuição).

## Decisões e limites aprovados

- Operação local entre CLIs/UIs, sem dependência do Orca; os provedores podem alternar os papéis de maestra e worker.
- Modelos default aprovados pelo usuário (22/10/2026):
  - **Antigravity**: `gemini 3.8(high)` (`--model gemini-3.8-flash-high --effort high`).
  - **Codex**: `sol 6.1(low)` (`--model gpt-6.1-sol -c model_reasoning_effort="low"`).
- Política de sessão e visibilidade de chats nas UIs (22/10/2026):
  - Quando o usuário indicar **projeto + nome do chat**, a ponte resolve automaticamente o UUID da conversa existente naquele projeto (`--worker-project` + `--worker-chat`) e continua nela.
  - Quando o usuário **não indicar** um chat existente, a ponte assume criação de um **novo chat** e sincroniza automaticamente seus metadados locais (`source='vscode'`, `originator='Codex Desktop'`, `cwd` canônico do projeto em `~/.codex/state_5.sqlite` e `session_index.jsonl` para o Codex; sincronização de `.db` e `conversation_summaries.db` com `project_id` do workspace para o Antigravity) para que todos os chats fiquem visíveis nas interfaces gráficas para acompanhamento.
- Acompanhamento em tempo real (23/10/2026): as UIs do Codex e do Antigravity não exibem ao vivo conversas iniciadas pela ponte. Toda delegação passa a abrir automaticamente um terminal somente leitura que segue um `live.log` legível do worker e permanece aberto com o resumo final (R16, T08A). O histórico nas UIs continua sincronizado, mas não é o canal de acompanhamento. Modo `--watch-native` (padrão, Codex e Antigravity): abre a tela interativa da CLI do worker com a tarefa como primeiro prompt; a ponte devolve o resultado ao fim desse turno, validado pelos arquivos de sessão, e a janela segue aberta. Comprovado nas duas direções no T09. Fechar a janela antes do fim do turno interrompe a tarefa.
- Primeiros fluxos limitados a análise e relatórios. Não autorizar escrita em produto ou bancos de negócio.
- Validar por simulações e ensaios reais curtos, de baixo consumo; registrar modelos solicitados e observados, sem fallback silencioso.
- Para hosts sem canal de wakeup pós-turno comprovado, usar a chamada síncrona da ponte: o processo aguarda o worker sem inferência ou polling de LLM e retorna o resultado estruturado no `stdout` (demonstrado no T09).
- Para AGY headless, a T01 aprovada esvaziou temporariamente `mcpServers` na configuração global durante a execução e restaurou os bytes originais, conferindo SHA-256. Assim, **todos** os MCPs globais ficam indisponíveis para novas inicializações durante essa janela. O campo `disabled: true` e o override por workspace não isolaram MCPs nesta instalação. Desde a correção de T06, o erro de `restore()` é propagado e há lock entre processos (`.myo.lock`); no modo interativo (padrão) os MCPs não são isolados.
- As três conversas AGY que tiveram timeout em 19/10 foram descartadas, sem `resume`: `267226e8-c6ae-4932-a214-d65fbef02ba9`, `9a733531-7fc4-4d34-92ec-83bfb989df8c` e `17c851de-1c08-4514-820a-e8387b9f1f2d`. O quinto ensaio T01 posterior passou; o histórico permanece em `docs/spikes/native-return.md`.
- Toolchain observado: `go1.27.0 windows/amd64`. Versões de CLIs e suporte gráfico precisam ser reconfirmados quando cada adaptador/UI for validado.

## Próxima ação

v0.1.0 publicada em https://github.com/jovemcleb/simple-orchestrator; continuação de chat existente validada nos dois workers (v0.1.1, com recusa quando a conversa está aberta em outro app/janela). Próximo: colher retorno de testadores; depois T10 (adaptador Claude, ou outro provedor seguindo `CONTRIBUTING.md`), validar o Copilot quando houver conta com modelos explícitos e T12 (validação visual nas UIs Codex Desktop e Antigravity IDE como maestras). A partir do Codex Desktop, o `delegate` precisa rodar fora do sandbox (aprovação/escalação), conforme `docs/validation/first-pair.md`.

## Pendências posteriores

- T10 adaptador Claude; validação real do T11 (Copilot); T12 matriz de capacidades por CLI/UI. Codex Desktop, Antigravity IDE e cada CLI são superfícies distintas.
- Título do chat delegado não aplicado no índice da IDE do Antigravity; modelo observado `unknown` no AGY interativo.
- Confirmar modelos e permissões atuais por perfil antes de cada ensaio real.
- A documentação inicial do AGY por workspace e a auditoria do executável local são registradas como evidência histórica em `docs/spikes/native-return.md`; para o comportamento observado, prevalece o ensaio T01 aprovado com isolamento global temporário.
- Skills locais e critérios de uso estão descritos em `AGENTS.md` e `.agents/skills/sources.json`.

## Preferências

Economizar contexto, dividir entregas substanciais em fases, preservar trabalho existente e distinguir implementação local de validação real, visual ou de publicação.
