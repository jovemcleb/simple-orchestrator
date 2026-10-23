---
name: local-agent-flow
description: Delegar tarefas entre Codex e Antigravity na mesma máquina usando a ponte local simple-orchestrator. Use quando o usuário pedir para delegar, passar uma tarefa, pedir uma revisão ou análise a outro agente local (Codex ou Antigravity) e receber o resultado de volta neste chat.
---

# Local Agent Flow (Codex ⇄ Antigravity)

Use esta skill quando o usuário pedir para **delegar uma tarefa a outro agente local** (Codex ou Antigravity) e receber o resultado de volta neste mesmo chat, pela ponte `simple-orchestrator`.

Binário instalado por padrão em:

```powershell
"$env:LOCALAPPDATA\Programs\simple-orchestrator\simple-orchestrator.exe"
```

## 1. Montar um plano curto

1. Se o objetivo, o escopo, os limites de escrita ou o critério de aceite não estiverem claros, pergunte ao usuário, **uma pergunta por vez**, antes de delegar. Se a skill `grill-me` estiver disponível, use-a.
2. Defina:
   - `--target`: arquivo, pasta ou escopo.
   - `--change`: o que o worker deve fazer.
   - `--constraints`: limites (por padrão, **somente leitura**; só autorize escrita em arquivos específicos quando o usuário pedir).
   - `--acceptance`: como saber que deu certo (algo observável).
   - Worker (`codex` ou `antigravity`) e, se o usuário pedir, modelo e esforço.
3. Nunca acesse banco de dados de negócio real nem faça push/deploy sem autorização explícita.

## 2. Identificar a sessão atual (nunca use "latest")

- **Codex**: use o valor real de `$env:CODEX_THREAD_ID`.
- **Antigravity**: use o `Conversation ID` informado em `<user_information>`.
- Nunca passe `latest`, `last` ou `ultima` como ID: a ponte rejeita sessões ambíguas.

Modelos padrão (quando o usuário não escolher):

| Worker | Padrão | Observação |
| --- | --- | --- |
| Antigravity | `gemini-3.8-flash-high`, esforço `high` | No `agy -p`, esse modelo só aceita `--effort high`. |
| Codex | `gpt-6.1-sol`, esforço `low` | Aceita `--model` e `--effort` explícitos. |

Chat do worker:
- **Continuar um chat existente**: passe `--worker-project "<projeto>"` e `--worker-chat "<nome do chat>"`, ou `--worker-session "<ID>"`. O chat **não pode estar aberto em outro lugar**: no Codex, feche-o no Codex Desktop, no VS Code ou em outra janela do Codex (o Codex só permite um app por conversa); no Antigravity, feche outras janelas do `agy` e a conversa na IDE (duas instâncias sobrescrevem o histórico). A ponte recusa na hora quando detecta isso; explique ao usuário o que fechar.
- **Criar um chat novo** (padrão): omita esses campos e, se quiser, passe `--worker-chat-title "<título>"`. A ponte registra o chat no histórico do Codex ou do Antigravity. No Codex Desktop, o chat novo só aparece na barra lateral depois de recarregar o app.

## 3. Delegar e aguardar o retorno

```powershell
& "$env:LOCALAPPDATA\Programs\simple-orchestrator\simple-orchestrator.exe" delegate `
  --run-key "<chave-unica-do-fluxo>" `
  --task-id "task-01" `
  --attempt-id "att-01" `
  --maestra-provider "codex" `
  --maestra-session "<ID-DA-SESSAO-ATUAL>" `
  --worker-provider "antigravity" `
  --workspace "<pasta do projeto>" `
  --target "<alvo>" `
  --change "<o que fazer>" `
  --constraints "Somente leitura; não alterar código" `
  --acceptance "<critério observável>" `
  --prompt "<instruções adicionais>" `
  --auto-ack
```

- Para o sentido inverso (Antigravity delegando ao Codex), troque `--maestra-provider "antigravity"` e `--worker-provider "codex"`.
- **Não passe `--timeout`** a menos que o usuário peça: a ponte espera o fim do primeiro turno do worker.
- O resultado volta como JSON no `stdout` (código de saída `0`): confira `ok`, `event.summary`, `event.observed_model` e `inference_calls_during_wait` (deve ser `0`). Em falha (código `1`), leia `event.summary` no `stdout`; erros de validação saem como JSON no `stderr`. Apresente o resultado ao usuário neste chat.

### Como esperar sem gastar inferência

- **Codex**: execute o `delegate` e aguarde o comando terminar, sem polling paralelo. **Execute o comando fora do sandbox** (peça aprovação ou escalação): dentro do sandbox a ponte não consegue gravar o estado em `%LOCALAPPDATA%\SimpleOrchestrator` nem abrir a janela do worker (`Access is denied`).
- **Antigravity**: se o `run_command` passar o comando para segundo plano, **encerre o turno** sem ficar consultando o status; o Antigravity acorda o chat quando o comando terminar.

### Acompanhamento ao vivo (padrão)

- Toda delegação abre uma janela com a **tela interativa da CLI do worker** (`codex` ou `agy -i`), já com a tarefa como primeiro prompt. O usuário acompanha tudo ao vivo.
- A ponte devolve o resultado quando esse primeiro turno termina, e a janela continua aberta. O que o usuário digitar nela depois não entra no resultado.
- **Não feche a janela antes do fim do turno**: isso interrompe a tarefa e a ponte retorna falha com o motivo.
- Se a CLI do worker mostrar um diálogo (aviso de limite de uso, confirmação, login), a tarefa fica parada até o usuário responder na janela. Avise o usuário se a espera demorar.
- `--watch-native=false`: worker sem tela (stream estruturado) e uma janela separada, somente leitura, com o log traduzido. Fechar essa janela não afeta a tarefa.
- `--no-watch`: worker sem tela e nenhuma janela. Use quando ninguém for acompanhar.
- Nos dois modos sem tela, o worker Antigravity esvazia temporariamente os MCPs globais de `~/.gemini/config/mcp_config.json` e restaura os bytes exatos ao terminar (conferidos por SHA-256).
- Em todos os modos, o log legível fica em `%LOCALAPPDATA%\SimpleOrchestrator\live\<run_id>\<task_id>-<attempt_id>.live.log`.

## 4. Confirmar e encerrar

Com `--auto-ack`, o recebimento já fica confirmado. Sem ele, depois de validar o resultado contra o critério de aceite:

```powershell
$bin = "$env:LOCALAPPDATA\Programs\simple-orchestrator\simple-orchestrator.exe"
& $bin ack --run-id "<run_id>" --task-id "task-01" --attempt-id "att-01"
& $bin finish --run-id "<run_id>"
```

Para consultar ou recuperar uma execução interrompida:

```powershell
& $bin status --run-id "<run_id>" --recover
```

## Limites

- Nesta versão, o par suportado é **Codex ⇄ Antigravity**. O worker GitHub Copilot é experimental e fica desligado (`--experimental-copilot`).
- Os workers rodam sem pedir aprovação a cada comando (`--ask-for-approval never` no Codex, `--dangerously-skip-permissions` no Antigravity). Use em projetos confiáveis e mantenha `--constraints` restritivo.
