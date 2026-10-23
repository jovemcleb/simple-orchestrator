# Tarefas propostas

Status em 23/10/2026: **T01–T09 concluídas, incluindo T08A** (acompanhamento ao vivo com a tela interativa da CLI do worker como padrão). T09 validado nas duas direções com evidência em `docs/validation/first-pair.md`. **T11 implementada como experimental** (adaptador Copilot com testes; ensaio real bloqueado por falta de acesso a modelos explícitos na CLI; desligado por padrão com `--experimental-copilot`). Pendentes: **T10** e **T12**. Status anterior, de 21/10/2026: **T01–T08 concluídas, revisadas por `gpt-6-luna` (`01a15543-4df3-78b3-853e-3dd07a139b28`) e com o aceite de restauração de T06 fechado** (`RunTask` agora propaga qualquer erro de `restore()`, coordena entre processos via `.myo.lock` e possui teste `TestAdapter_RunTask_PropagatesRestoreError`; `go vet ./...` e `go test -v ./...` passando nos 6 pacotes; binário atualizado em `bin/simple-orchestrator.exe`). Pronto para iniciar **T09**. Referências: `spec.md`, `design.md`, `.specs/project/STATE.md` e `docs/spikes/native-return.md`.

## Pré-condições

- Go confirmado em 21/10/2026: `go version go1.27.0 windows/amd64` em `C:/Program Files/Go/bin/go.exe`.
- Registrar CLI, modelos e sessão de retorno explícitos para cada ensaio real. Ajuda/versão não substituem teste de autenticação.
- Congelar limites do primeiro ensaio: uma maestra, um worker, uma tarefa sem dados de negócio, uma consolidação, sem perguntas bloqueantes ou delegação recursiva. Duração máxima e eventuais chamadas de recuperação serão escolhidas antes da execução.

## Ordem e gates

Todos os itens abaixo são sequenciais por padrão; priorizar completude de um fluxo antes de ativar mais provedores. Cada task tem um responsável por seu entregável e respectivos testes, preservando alterações não relacionadas.

| ID | Entregável / onde proposto | Depende de | Aceite / requisitos | Testes junto à tarefa |
| --- | --- | --- | --- | --- |
| T01 | Relatório do spike de retorno ao host em `docs/spikes/native-return.md` | — | Identificação de sessão, evidência de bloqueio do socket externo no Codex Desktop, decisão aprovada de retorno síncrono no processo da ponte e aprovação do worker AGY com auto-skip do `open-design` (restauração SHA-256 verificada); R02/R08/R14. | 1 ensaio curto autorizado com `open-design` auto-skip (`MYO_T01_WORKER_OK`) + conferência SHA-256 de `~/.gemini/config/mcp_config.json`. |
| T02 | Contrato normalizado de eventos em `internal/protocol` | T01 | IDs, schema, papéis, status e validação; R03/R04/R05/R07. | Unitários: evento malformado, origem/destino errados, campos de modelo desconhecido; `go test ./...` + `go vet ./...`. |
| T03 | Diário/outbox em `internal/store` | T02 | Gravar antes de enviar, recuperar, distinguir duplicata e entrega ambígua; R01/R09/R11. | Unitários/integração temporária: duplicatas, registro truncado, crash antes/depois do envio. Mesmos gates. |
| T04 | Máquina de estados em `internal/runtime` | T03 | Dispatch, espera sem inferência (incluindo espera bloqueante de processo com retorno estruturado no stdout), perguntas pendentes, finalização explícita, autoencerramento; R06/R07/R10/R11/R15. | Integração simulada: zero chamadas durante espera, corrida com encerramento, processo ainda ativo, retorno fora de ordem. Mesmos gates. |
| T05 | Adaptador Codex em `internal/providers/codex` | T04 | Worker via `codex exec --json` / `resume` e suporte a maestra via retorno síncrono no processo da ponte (e `codex queue` apenas onde o daemon estiver acessível); R02/R03/R04/R08/R12/R14. | Fixtures JSONL/erros/duplicatas; simular retorno síncrono no stdout e queue onde aplicável. Mesmos gates. |
| T06 | Adaptador Antigravity CLI em `internal/providers/antigravity` | T05 | Entrada/resultado estruturado, continuação por ID, modelo, falhas e isolamento temporário da configuração global MCP para o worker headless, com restauração SHA-256 e propagação de erro em `RunTask` + lock entre processos `.myo.lock`; R03/R04/R07/R12/R15. | Fixtures stream-json: sucesso, falha, EOF incompleto, parser incremental, uso ausente e testes unitários `TestIsolateGlobalMCPConfig` e `TestAdapter_RunTask_PropagatesRestoreError`. Registrar que o isolamento afeta todos os MCPs globais durante a janela. |
| T07 | Comando de entrada em `cmd/orchestrator` | T06 | Iniciar/reutilizar execução, suportar delegação com espera bloqueante no processo e oferecer status/ack/finalização/retomada; R01/R02/R06/R08/R10/R11. | Integração por processo fake: duas inicializações, caminho com espaços, entrada inválida, saída limpa e estado persistido. Mesmos gates. |
| T08 | Skill portátil em `skills/local-agent-flow` | T07 | Planejar com grill-me, perfis e evidências; chamar comandos reais (`Codex ⇄ Antigravity`), registrar sessão e processar retorno; R05/R13/R14. | Inspeção com skill-creator e CLI real `--help`; sem instalar nos hosts automaticamente. |
| T08A | Acompanhamento ao vivo por terminal em `internal/monitor` (log legível + abertura de terminal) integrado aos adaptadores Codex e Antigravity | T08 | Ao iniciar worker, gravar `live.log` legível e abrir terminal automaticamente (Windows Terminal ou fallback PowerShell) que segue o log e permanece aberto com resumo/status final; fechar o terminal não afeta a tarefa; falha ao abrir é registrada sem bloquear; R16/R06/R10. | Unitários: formatação de eventos Codex/AGY em linhas legíveis, escrita append e resumo final; abertura de terminal via executor injetável (sem abrir janela real nos testes), fallback e falha não bloqueante. Mesmos gates. |
| T09 | Relatório do fluxo completo real em `docs/validation/first-pair.md` | T08A | Delegação bidirecional validada (`Codex -> AGY` e `AGY -> Codex`), persistência no diário/outbox, retorno estruturado sem inferência na espera e desligamento limpo da ponte; R01–R15 no escopo do primeiro par. | Ensaio real curto; registrar uso exposto, falhas e limitações. Nenhuma tarefa de negócio ou escrita em produto. |
| T10 | Adaptador Claude em `internal/providers/claude` | T09 | Worker e maestra CLI com modelo/sessão e retorno suportados; UI exige gate próprio; R02/R03/R04/R07/R08/R12/R14/R15. | Unitários/fixtures na tarefa + teste curto de papel com sessão isolada. Mesmos gates. |
| T11 | Adaptador Copilot em `internal/providers/copilot` | T10 | Escolher CLI estruturada ou SDK Go conforme compatibilidade; distinguir runtime e UI; mesmos requisitos de T10. | Fixtures e teste curto por papel. Se SDK escolhido, verificar permissões e eventos antes de ativar. Mesmos gates. |
| T12 | Matriz final de capacidades em `docs/validation/provider-matrix.md` | T11 | Cada provedor demonstrado como maestra e worker; cada UI nomeada com suporte, bloqueio ou não validado. Não declarar R03 completo sem os quatro. | Testar lacunas da matriz, não repetir todos os ensaios já comprovados. Falhas conhecidas permanecem explícitas. |

## Verificação de dependências

Representação: T01 → T02 → T03 → T04 → T05 → T06 → T07 → T08 → T08A → T09 → T10 → T11 → T12.

| Faixa | Predecessor na sequência | Campo Depende de | Resultado |
| --- | --- | --- | --- |
| T01 | Nenhum | — | Confere |
| T02–T12 (incluindo T08A) | Item imediatamente anterior | Item imediatamente anterior em todas as linhas | Confere |

Sem referências futuras e sem dependências circulares. T01 é gate de viabilidade antes de construir a infraestrutura completa.

## Verificação de testes e granularidade

| Tarefa | Camada do design | Tipo exigido = tipo planejado | Um entregável por tarefa |
| --- | --- | --- | --- |
| T01 | Host nativo | Inspeção + ensaio real curto | Sim: relatório de viabilidade |
| T02 | Contratos | Unitário | Sim: contrato e testes |
| T03 | Persistência | Unitário + integração temporária | Sim: store e testes |
| T04 | Runtime | Integração simulada | Sim: máquina de estados e testes |
| T05/T06/T10/T11 | Adaptadores | Fixtures/unitário + integração pertinente | Sim: um adaptador em cada tarefa |
| T07 | CLI | Integração simulada | Sim: entrada CLI e testes |
| T08A | Monitor | Unitário com executor injetável | Sim: monitor e testes |
| T08 | Skill | Inspeção de comandos/escopo | Sim: skill operacional |
| T09/T12 | Validação nativa | Ensaio real e evidência | Sim: um relatório por tarefa |

Teste via CLI headless não encerra automaticamente a validação da UI. Caso o spike bloqueie o alvo preferido, atualizar a spec com o usuário antes de trocar de experiência.

## Rastreabilidade

| Requisitos | Tarefas |
| --- | --- |
| R01/R09/R11 | T03/T04/T07/T09 |
| R02/R08/R14 | T01/T05/T07/T08/T09/T10/T11/T12 |
| R03/R04 | T02/T05/T06/T09/T10/T11/T12 |
| R05/R13 | T02/T08/T09 |
| R06/R07/R10/R15 | T04/T05/T06/T07/T09/T10/T11 |
| R12 | T05/T06/T09/T10/T11/T12 |
| R16 | T08A/T09 |
