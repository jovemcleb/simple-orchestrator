# Pesquisa e discovery — 16/10/2026

## Atualização: skills adicionadas ao projeto

Após autorização do usuário, instaladas em `.agents/skills/`: `desenha-sistema`, `go-concurrency-patterns` (wshobson/agents, revisão fixada) e `golang-testing` (samber/cc-skills-golang). A desenha-sistema inclui as três referências e os dois scripts originais; o SKILL.md recebeu orientação local para arquitetura proposta e uso por outros agentes.

Origens e hashes em `.agents/skills/sources.json`; seleção em `AGENTS.md`, com ponteiros para Claude e Copilot. Geração SVG/HTML/Excalidraw e validador Excalidraw passaram em fixture temporária. A detecção de segredos pode bloquear caminhos relativos longos; usar referência curta verificável sem desabilitar a proteção. Não foi realizada revisão visual do desenho de teste. As skills Go são orientações instaladas; implementação da ponte e validação Go continuam pendentes.

O restante deste documento registra a pesquisa anterior à instalação.

## Método e limites

Pasta nova sem código ou documentos anteriores. Consultadas as skills locais e o orca-flow apenas como inspiração. Context7 não está disponível na sessão; depois da inspeção local, consultadas fontes públicas oficiais. Nenhum modelo executado, login alterado ou ponte instalada. Ajuda/versão não comprovam autenticação nem funcionamento de inference.

## Evidências locais

| Provedor | Versão e caminho | Entrada/eventos/retomada observados | Limite |
| --- | --- | --- | --- |
| Codex | 0.162.1; `<npm global>/codex.ps1` | `exec --json`, `exec resume`, `app-server`, `queue --thread --message`; `--model` | Queue não foi executado; entrega/retomada ao desktop ainda não testadas |
| Claude Code | 2.1.89; `%USERPROFILE%/.local/bin/claude.exe` | `--print`, `--input-format stream-json`, `--output-format stream-json`, `--resume`, `--model`, `--effort` | CLI programática não certifica retorno ao Claude Desktop/IDE |
| GitHub Copilot | 1.0.98; `<npm global>/copilot.ps1` | `--prompt`, `--output-format json`, `--resume`, `--session-id`, `--model`, `--reasoning-effort`, `--acp` | Falha inicial EPERM na extração do runtime; ajuda/versão funcionaram em execução com permissão adequada. UI e modelos efetivos não testados |
| Antigravity CLI | 1.4.0; `%USERPROFILE%/AppData/Local/agy/bin/agy.EXE` | `--print`, `--input-format stream-json`, `--output-format stream-json`, `--conversation`, `--model`, `--effort`, remote-control | Não inferir que a CLI permite injetar mensagem em qualquer chat da IDE |

Comandos executados: versões e helps de cada CLI; subhelps de `codex exec`, `exec resume`, `queue`, `app-server`, `agents`; help do remote-control AGY. Login/cotas e listas efetivas de modelos ficaram desconhecidos.

## Fontes oficiais e decisões

- [Codex developer commands](https://learn.chatgpt.com/docs/developer-commands): documenta `codex queue --thread <SESSION_ID> --message ...`; usar UUID explícito. É candidato a retorno sem Orca, sujeito ao teste do host.
- [Codex app-server](https://learn.chatgpt.com/docs/app-server): protocolo para threads, turnos e eventos; `thread/resume` preserva sessão, não é prova de processamento de um novo retorno. Evitar abrir um segundo dono da mesma sessão por conta própria.
- [Antigravity CLI — repositório oficial](https://github.com/google-antigravity/antigravity-cli): confirma produto CLI e sessões. [Modo headless](https://www.agy.dev/docs/cli/headless/) descreve prompts NDJSON e resultado por turno; usar somente o formato compatível com a versão instalada. Resume de conversa é diferente de injetar entrada na UI.
- [Claude programático](https://code.claude.com/docs/en/headless): documenta print, saída estruturada e continuidade. Documentação atual pode conter recursos posteriores à versão local 2.1.89; flags e comportamento exigem verificação local antes de adotar.
- [Copilot eventos](https://docs.github.com/en/copilot/how-tos/copilot-sdk/features/streaming-events): distinguir mensagens do fim do processamento. `session.idle` é efêmero; assinar antes de iniciar/retomar. Validar resultado/erros além do idle.
- [Copilot SDK Go](https://github.com/github/copilot-sdk/blob/main/go/README.md): opção oficial para o adaptador, atualmente pede Go 1.24+ e CLI disponível. Fixar versões na implementação; não copiar requisitos de guias antigos.
- [Go os/exec](https://pkg.go.dev/os/exec): processos filhos via argumentos e pipes; tratar cancelamento, drenagem de saída e encerramento separadamente.

## Skills

`find-skills` já instalada em `%USERPROFILE%/.agents/skills/find-skills/SKILL.md`; não precisa reinstalar para pesquisar. O lock global não retornou entrada de origem dessa skill. Nome e conteúdo local confirmados; origem exata não certificada.

Busca real concluída: `node.exe .../npm/bin/npx-cli.js --yes skills find golang`. As tentativas iniciais por wrapper não concluíram; encerradas antes da busca direta. Nenhuma skill nova instalada.

| Skill | Uso proposto | Fonte/evidência | Decisão |
| --- | --- | --- | --- |
| tlc-spec-driven | Spec, roadmap, estado e tarefas | Instalada; explicitamente solicitada | Usar neste planejamento |
| grill-me | Resolver decisões do fluxo, uma pergunta de cada vez | Instalada e lida | Reusar; sem entrevista repetida |
| find-skills | Buscar complementos pontuais | Instalada e usada | Reusar; não duplicar instalação |
| orca-flow | Inspiração para fluxo, perfis, evidências e retomada | Instalada e lida | Não importar dependência de runtime Orca |
| skill-creator | Criar skill portátil para as quatro IAs | Já disponível | Carregar ao criar, depois do contrato da ponte |
| go-concurrency-patterns | Cancelamento, goroutines e desligamento | [skills.sh](https://www.skills.sh/wshobson/agents/go-concurrency-patterns), fonte [wshobson/agents](https://github.com/wshobson/agents); 10,8 mil instalações no snapshot, repositório acima de 40 mil estrelas | Complemento candidato; ler arquivo integral e referências antes de instalar |
| golang-pro | Orientações gerais Go | [jeffallan/claude-skills](https://www.skills.sh/jeffallan/claude-skills/golang-pro); cerca de 18,7 mil instalações, repositório 11,9 mil estrelas | Mais amplo e sobreposto; não necessário agora. Catálogo mostra resultados divergentes de auditoria; popularidade não certifica qualidade |

Outra busca Go encontrou `samber/cc-skills-golang` com skills específicas de concorrência, contexto e testes. Catálogo e repositório consultados, mas arquivo integral não revisado; não adicionar um pacote inteiro por padrão.

Comando de instalação de um complemento candidato, se escolhido depois da inspeção: `npx skills add wshobson/agents --skill go-concurrency-patterns`. Não executado neste planejamento.

O comando indicado pelo usuário corresponde à skill find-skills; forma explícita documentada usa `--skill`: `npx skills add vercel-labs/skills --skill find-skills`. Não executar novamente apenas para resolver a dúvida de presença.
