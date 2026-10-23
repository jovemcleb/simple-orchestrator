# Simple Orchestrator

Delegue tarefas entre o **Codex** e o **Antigravity** na sua máquina e acompanhe o outro agente trabalhando **ao vivo**, na tela da própria CLI. Quando ele termina, o resultado volta sozinho para o chat que pediu.

```text
Você, no chat do Codex:  "delegue ao Antigravity: revise o README e liste 3 melhorias"
        │
        ▼
 skill local-agent-flow ──► simple-orchestrator delegate
                                   │
                                   ├─► abre uma janela com o `agy` já executando a tarefa (você assiste)
                                   │
                                   └─◄ fim do turno: o resultado volta como JSON para o chat do Codex
                                       (a janela continua aberta para você ler)
```

Funciona nos dois sentidos: o Codex delega ao Antigravity, e o Antigravity delega ao Codex.

> **Estado:** primeira versão (v0.1). Windows apenas. Validado com Codex CLI 0.163–0.164 e Antigravity CLI 1.4.6. Veja [Limitações](#limitações).

## Requisitos

| Requisito | Para quê | Como conferir |
| --- | --- | --- |
| Windows 10 ou 11 | A ponte usa APIs do Windows para abrir as janelas. | — |
| [Node.js](https://nodejs.org/) (com npm) | O Codex CLI é instalado pelo npm. | `node --version` |
| [Codex CLI](https://github.com/openai/codex), logado | Worker e maestra Codex. Instale com `npm install -g @openai/codex` e faça `codex login`. | `codex --version` |
| Antigravity CLI (`agy`), logada | Worker e maestra Antigravity. Instale o [Google Antigravity](https://antigravity.google), faça login e, se `agy` não estiver no PATH, rode `& "$env:LOCALAPPDATA\agy\bin\agy.exe" install`. | `agy --version` |
| [Python 3](https://www.python.org/downloads/) no PATH | A ponte o usa para ler e atualizar os históricos do Codex e do Antigravity. | `python --version` |
| Windows Terminal (opcional) | Abas em vez do console clássico. | `wt --version` |

Go 1.27+ só é necessário para compilar a partir do código.

## Instalação

**Opção 1: um comando** (baixa a última release):

```powershell
irm https://raw.githubusercontent.com/jovemcleb/simple-orchestrator/main/install.ps1 | iex
```

**Opção 2: pela release.** Baixe `simple-orchestrator-windows-amd64.zip` em [Releases](https://github.com/jovemcleb/simple-orchestrator/releases), extraia e rode `.\install.ps1`.

**Opção 3: pelo código** (requer Go 1.27+):

```powershell
git clone https://github.com/jovemcleb/simple-orchestrator
cd simple-orchestrator
.\install.ps1
```

O instalador:
1. coloca `simple-orchestrator.exe` em `%LOCALAPPDATA%\Programs\simple-orchestrator` e adiciona essa pasta ao seu PATH;
2. copia a skill `local-agent-flow` para as pastas de skills do Codex (`~/.codex/skills`, `~/.agents/skills`) e do Antigravity (`~/.gemini/antigravity/skills`, `~/.gemini/skills`) que existirem, guardando cópias antigas em `...\simple-orchestrator\backups`;
3. confere os pré-requisitos e avisa o que falta (os avisos não interrompem a instalação).

Depois, **reinicie o Codex e o Antigravity** para eles carregarem a skill.

### Conferir a instalação

Num terminal **novo** (para o PATH atualizado valer):

```powershell
simple-orchestrator version   # deve mostrar a versão, ex.: simple-orchestrator v0.1.0
codex --version
agy --version
python --version
```

Se tudo responder, faça o primeiro uso abaixo com uma tarefa pequena. Se o worker responder e o resultado voltar ao chat, está funcionando.

## Primeiro uso

No chat do **Codex** (Desktop, extensão do VS Code ou CLI):

> Use a skill local-agent-flow e delegue ao Antigravity: leia o README.md e liste 3 melhorias. Somente leitura.

No chat do **Antigravity**:

> Use a skill local-agent-flow e delegue ao Codex: revise `src/app.ts` e aponte possíveis bugs. Somente leitura.

O que acontece:
1. O agente monta a tarefa (alvo, objetivo, limites e critério de aceite) e chama `simple-orchestrator delegate`, informando o ID da conversa atual (a skill ensina onde achá-lo).
2. **No Codex, aprove a execução fora do sandbox** quando ele pedir. Dentro do sandbox a ponte não consegue gravar seu estado nem abrir a janela.
3. Abre uma janela com a tela interativa do outro agente, já trabalhando na tarefa.
4. Quando o primeiro turno dele termina, o comando devolve um JSON com o resultado, e o agente que pediu o lê e responde no seu chat. A janela continua aberta; o que você digitar nela depois não entra no resultado.

### Chat novo ou chat existente

Você decide no pedido:
- **Chat novo** (padrão): não diga nada, ou dê um título ("…num chat chamado 'Revisão da API'"). O chat é registrado no histórico do worker.
- **Continuar um chat existente**: diga o projeto e o nome do chat, ou o ID da conversa. Exemplo: *"delegue ao Antigravity, no chat 'Revisão da API' do projeto loja, …"*. A ponte acha a conversa e continua nela, com o contexto anterior.

Para continuar, **o chat não pode estar aberto em outro lugar**:
- **Codex:** o Codex só deixa um app usar cada conversa. Se ela estiver aberta no Codex Desktop, na extensão do VS Code ou em outra janela do Codex, a ponte recusa na hora e pede para fechar lá.
- **Antigravity:** o `agy` não impede duas janelas na mesma conversa, mas elas sobrescrevem o histórico uma da outra. A ponte recusa se houver outro `agy` aberto naquela conversa. Ela **não** detecta a conversa aberta na IDE do Antigravity; feche-a lá antes.

### Chamando direto do terminal

```powershell
simple-orchestrator delegate `
  --run-key "teste-1" --maestra-provider codex --maestra-session "teste-manual" `
  --worker-provider antigravity --workspace "C:\caminho\do\projeto" `
  --target "README.md" --change "Listar 3 melhorias" `
  --constraints "Somente leitura" --acceptance "Lista com 3 itens" --auto-ack
```

`--maestra-session` identifica quem recebe o resultado. Dentro do Codex é o `$env:CODEX_THREAD_ID`; no Antigravity, o Conversation ID da conversa. Numa chamada manual, use qualquer identificador seu (por exemplo, `teste-manual`), exceto `latest`, `last` ou `ultima`. Nesse caso o resultado fica só no terminal.

**Saída e códigos de retorno:**
- `0`: sucesso. O JSON do resultado sai no `stdout`.
- `1`: falha. Se o worker chegou a rodar, o JSON do evento (com `event.summary` explicando o motivo) sai no `stdout`; erros antes disso (argumentos inválidos, estado inacessível) saem como JSON no `stderr`.
- `2`: uso incorreto das opções.

Capture `stdout` e `stderr`.

## Acompanhamento ao vivo

| Modo | Como ativar | O que você vê |
| --- | --- | --- |
| **Tela interativa** (padrão) | nada | A CLI do worker (`codex` ou `agy -i`) numa janela, com a tarefa como primeiro prompt. **Não feche antes do fim do turno**: isso interrompe a tarefa. Se a CLI mostrar um diálogo (por exemplo, aviso de limite de uso ou confirmação), responda na janela; enquanto isso a tarefa fica parada. |
| Log traduzido | `--watch-native=false` | Worker sem tela e uma janela somente leitura com os eventos em texto. Fechar essa janela não afeta a tarefa. |
| Sem janela | `--no-watch` | Worker sem tela e nada abre; use quando ninguém for acompanhar. |

Em todos os modos, o log legível fica em `%LOCALAPPDATA%\SimpleOrchestrator\live\<run_id>\<task_id>-<attempt_id>.live.log`.

## Comandos

| Comando | Para que serve |
| --- | --- |
| `delegate` | Inicia (ou reaproveita) uma execução, roda o worker e devolve o resultado em JSON. |
| `status --run-id <id> [--recover]` | Mostra o estado salvo de uma execução e recupera após interrupção. Se você só tem o `--run-key`, o `run_id` correspondente está em `%LOCALAPPDATA%\SimpleOrchestrator\run_keys.json`. |
| `ack --run-id <id> --task-id <t> --attempt-id <a>` | Confirma que a maestra processou o resultado (ou use `--auto-ack` no `delegate`). |
| `finish --run-id <id>` | Encerra a execução depois que todas as entregas foram confirmadas. |
| `version` | Mostra a versão. |

Principais opções do `delegate` (veja todas com `simple-orchestrator delegate -h`):

| Opção | Descrição |
| --- | --- |
| `--maestra-provider`, `--maestra-session` | Quem pediu (`codex` ou `antigravity`) e o ID real da sessão que recebe o resultado. |
| `--worker-provider` | Quem executa: `codex` ou `antigravity`. |
| `--workspace` | Pasta do projeto onde o worker trabalha. |
| `--target`, `--change`, `--constraints`, `--acceptance`, `--prompt` | A tarefa: alvo, objetivo, limites, critério de aceite e instruções extras. |
| `--model`, `--effort` | Modelo e esforço do worker. Padrões: Codex `gpt-6.1-sol`/`low`; Antigravity `gemini-3.8-flash-high`/`high`. |
| `--read-only` | Codex em sandbox somente leitura. |
| `--worker-session` ou `--worker-project` + `--worker-chat` | Continua um chat existente do worker em vez de criar um novo. |
| `--worker-chat-title` | Título do chat novo. |
| `--timeout` | Limite em segundos (padrão: sem limite; espera o fim do turno). |
| `--auto-ack` | Confirma o recebimento automaticamente. |

O JSON de resposta traz `ok`, `event.summary` (a resposta do worker), `event.observed_model`, `event.usage` e `inference_calls_during_wait`, que deve ser sempre `0`: a ponte espera sem chamar nenhum modelo.

## O que a ponte faz na sua máquina

É importante saber antes de usar:

- **Workers sem confirmação por comando.** O Codex roda com `--ask-for-approval never`, e o Antigravity com `--dangerously-skip-permissions`. Use em projetos confiáveis e peça limites claros (por exemplo, "somente leitura").
- **Históricos dos apps.** Para o chat delegado aparecer no Codex e no Antigravity, a ponte atualiza os arquivos locais deles (`~/.codex/state_5.sqlite` e `session_index.jsonl`; `~/.gemini/antigravity*/conversation_summaries.db`). Esses formatos não são públicos e podem mudar em atualizações dos apps.
- **MCPs do Antigravity (só nos modos sem tela: `--watch-native=false` ou `--no-watch`).** Para o `agy` sem tela não travar, a ponte esvazia temporariamente `mcpServers` em `~/.gemini/config/mcp_config.json` e restaura os bytes originais no fim, conferindo o SHA-256. Durante essa janela, novos `agy` iniciam sem MCPs.
- **Janelas.** A ponte tenta criar a janela do worker fora do grupo de processos de quem chamou (direto ou via WMI), para ela continuar aberta depois que o comando termina. Se o Windows não permitir nenhum dos dois, a janela abre mesmo assim, mas fecha quando o comando da maestra terminar.
- **Estado.** Diário, outbox e logs ficam em `%LOCALAPPDATA%\SimpleOrchestrator`.

## Solução de problemas

| Sintoma | Causa e solução |
| --- | --- |
| `Access is denied` ao delegar a partir do Codex | O comando rodou dentro do sandbox. Aprove a execução fora do sandbox. |
| "o chat … do Codex está aberto em outro app" | A conversa que você quer continuar está aberta no Codex Desktop, no VS Code ou em outra janela do Codex. Feche-a lá, ou delegue para um chat novo. |
| "a conversa … do Antigravity já está aberta em outra janela do agy" | Feche a outra janela do `agy` nessa conversa, ou delegue para um chat novo. |
| A tarefa falhou com "janela fechada antes do turno terminar" | A janela do worker foi fechada no meio. Delegue de novo e deixe-a aberta até o resultado voltar. |
| A janela do worker está parada e nada acontece | Veja se a CLI mostra um diálogo (aviso de limite de uso, confirmação, login) e responda. |
| `codex` ou `agy` não encontrado | Instale a CLI e abra um terminal novo; confira com `codex --version` / `agy --version`. |
| O worker falha logo no início por autenticação | Faça login de novo na CLI (`codex login`, ou abra o `agy` e entre na conta). |
| O chat delegado não aparece no Codex Desktop | O Desktop mantém a lista em memória; recarregue ou reinicie o app. |
| `--model gemini-3.8-flash-high conflicts with --effort` | No `agy -p`, esse modelo só aceita `--effort high`. |
| A skill não aparece no host | Reinicie o app depois de instalar. Se o host usar outra pasta de skills, copie `skills/local-agent-flow` para ela. |
| `python` não encontrado | Instale o Python 3 e deixe-o no PATH. |
| Quero ver o que aconteceu | Abra o log em `%LOCALAPPDATA%\SimpleOrchestrator\live\<run_id>\`. |

## Limitações

- Par suportado: **Codex ⇄ Antigravity**, no Windows.
- A ponte devolve o resultado do **primeiro turno** do worker; perguntas do worker com ida e volta e vários workers em paralelo ainda não existem.
- No Antigravity interativo, o modelo observado aparece como `unknown` (o transcript não registra o modelo).
- O título dado ao chat delegado ainda não é aplicado no índice da IDE do Antigravity.
- **GitHub Copilot (experimental):** existe um adaptador (`--experimental-copilot`, com `--model` e `--effort` obrigatórios), mas ele só funciona se a CLI do Copilot estiver logada numa conta que libere modelos explícitos. Se a CLI trocar o modelo pedido por outro, a ponte falha em vez de aceitar a troca.

## Desinstalar

```powershell
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/jovemcleb/simple-orchestrator/main/install.ps1))) -Uninstall
```

Ou, numa pasta com o `install.ps1`: `.\install.ps1 -Uninstall`. Adicione `-Purge` para apagar também o estado em `%LOCALAPPDATA%\SimpleOrchestrator`.

## Contribuir

Quer adicionar o Claude Code, o OpenCode ou outra CLI? Leia o [CONTRIBUTING.md](CONTRIBUTING.md), que explica como criar um provedor seguindo o mesmo padrão. O planejamento e as decisões estão em [.specs/](.specs/), e as validações reais em [docs/validation/](docs/validation/).

## Licença

[MIT](LICENSE). As skills de terceiros em `.agents/skills/` mantêm as licenças originais (veja os arquivos `LICENSE` de cada uma).
