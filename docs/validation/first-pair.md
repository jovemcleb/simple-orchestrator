# T09 — Validação real do primeiro par (Codex ⇄ Antigravity)

Data: 23/10/2026. Escopo: uma maestra, um worker, uma tarefa sem dados de negócio, sem escrita em produto, retorno síncrono no `stdout` da ponte. Todos os workers rodaram com o padrão atual `--watch-native` (tela interativa da CLI do worker em janela própria).

## Resultado

| Direção | Host da maestra | Worker | Resultado | Retorno à maestra | Inferências na espera |
| --- | --- | --- | --- | --- | --- |
| Codex → AGY | `codex exec` (`gpt-6.1-sol`, low) | `agy -i` (`gemini-3.8-flash-high`) | `MYO_T09_AGY_OK` | `destination_session_id` = sessão da maestra Codex | 0 |
| AGY → Codex | `agy -p` (`gemini-3.8-flash-high`, high) | `codex` TUI (`gpt-6.1-sol`, low, read-only) | `MYO_T09_CODEX_OK` | `destination_session_id` = conversa da maestra AGY | 0 |

Nas duas direções a janela interativa do worker abriu sozinha, a ponte devolveu o JSON ao fim do primeiro turno e a janela continuou aberta depois do fim do comando da maestra.

Ensaios de apoio na mesma data, chamando a ponte direto do terminal: Codex TUI `MYO_TUI_OK` (44 s), AGY `agy -i` `MYO_AGY_TUI_OK` (16 s).

## Achados e correções

1. **Sandbox do Codex bloqueia a ponte.** Rodando no sandbox padrão, o `delegate` falhou com `mkdir %LOCALAPPDATA%\SimpleOrchestrator\runs\...: Access is denied`. A ponte precisa gravar estado fora do workspace, abrir janelas e escrever nos históricos das UIs. Com o comando executado fora do sandbox (equivalente a aprovar a escalação no Codex Desktop), passou. A skill passou a instruir o pedido de escalação para o `delegate`.
2. **Antigravity encerra a janela do worker junto com o comando.** O `run_command` do AGY roda comandos num job do Windows que nega `CREATE_BREAKAWAY_FROM_JOB` (`Access is denied`, confirmado com um executável de diagnóstico); a janela do Codex morria quando o comando da maestra terminava. Correção: quando o breakaway é negado, a ponte cria a janela via WMI (`Win32_Process.Create`), fora do job de quem chamou, com um wrapper PowerShell que repassa os argumentos já escapados (`ProcessStartInfo.Arguments`) e define `CI=true`. Reensaio: a janela do Codex permaneceu viva após o fim do `agy -p`.
3. **TUI exige console real.** `os/exec` sempre redireciona o stdin para `NUL`, e o Codex TUI recusa (`stdin is not a terminal`). A janela é criada com `CreateProcess` direto, sem redirecionar handles.
4. **`agy` em modo `-p`:** `--mode auto` não está disponível para esta conta (segue no modo padrão, com aviso), e `--model gemini-3.8-flash-high` exige `--effort high` (o modo interativo aceitou `--effort low`).
5. **Fim de turno no AGY interativo:** não há evento explícito. A ponte exige, ao mesmo tempo, `CASCADE_RUN_STATUS_IDLE` e `not_fully_idle = 0` em `conversation_summaries.db` e uma resposta final do modelo (sem tool calls) no `transcript.jsonl` depois do prompt delegado. Observado: `RUNNING/1` → `IDLE/0` em ~3 s após a resposta.

## Tempo e consumo

- Worker Codex: ~34 s por turno simples; ~26 mil tokens de entrada (contexto padrão do Codex), metade em cache.
- Worker AGY: ~7–16 s; ~9 mil tokens de entrada.
- Maestra AGY headless (`agy -p`): ~3 min até disparar o `delegate` e ~30 s para retomar após o fim do worker; o custo está na inicialização/planejamento da maestra, não na ponte.
- Modelo observado no AGY interativo: `unknown` (o transcript não registra o modelo).

## Limites e pendências

- As maestras foram as CLIs (`codex exec`, `agy -p`), não as UIs Codex Desktop e Antigravity IDE. Os mecanismos testados (sandbox do Codex, `run_command`/job do AGY) são os mesmos que as UIs usam para comandos, mas a validação visual nas UIs continua como gate próprio (T12).
- O que o usuário digitar na janela depois do primeiro turno não entra no resultado.
- Uso de tokens só é informado em chats novos (os contadores são cumulativos por sessão).
