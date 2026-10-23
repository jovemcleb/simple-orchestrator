# T01 — retorno ao chat de origem

> **Estado atualizado em 21/10/2026:** o worker AGY headless passou no quinto ensaio de protocolo, com restauração global verificada pelo script Python. O código Go da adaptação usa caminho de restauração separado; uma revisão identificou que `RunTask` descarta erro de restauração e que a serialização não cobre outros processos AGY. Corrigir esses pontos antes de tratar T06 como totalmente aceita ou iniciar T09. O retorno assíncrono à UI Codex não foi demonstrado; a decisão arquitetural aprovada usa retorno síncrono no `stdout` do processo da ponte. As seções de 19/10 abaixo registram a investigação histórica e seus três timeouts; as decisões posteriores e o quinto ensaio substituem seus vereditos provisórios.

Data: 19/10/2026 (atualizado e concluído em 21/10/2026). Estado: **T01 concluída e aprovada** (`worker_succeeded=true` em 5,35s com `MYO_T01_WORKER_OK`; configuração MCP centralizada e restaurada com SHA-256 idêntico; arquitetura de retorno síncrono no processo da ponte aprovada para `Codex ⇄ Antigravity`).

## Contrato e limites

- Sessão de origem: `01a140a8-f2e6-7552-bea5-4ba0a7e00660`, obtida de `CODEX_THREAD_ID` e confirmada no inventário do app. Sem seleção por sessão mais recente.
- Uma execução AGY, um turno com resposta exata, sem ferramentas ou dados de negócio. Perfil padrão existente, sem mudança de modelo/esforço.
- Timeout interno de 60 segundos, externo de 75 segundos. Nenhum retry de inferência ou fallback automático.
- Sem criar nova conversa/daemon, reiniciar aplicativos ou alterar credenciais/configuração.
- O script Python é apenas instrumento descartável do spike; não é o núcleo Go da ponte.

## Evidência anterior ao worker

Codex CLI 0.162.1 e AGY 1.4.0. A ajuda local confirma `codex queue --thread UUID --message TEXT` e AGY print/stream-json. A documentação oficial de [developer commands](https://learn.chatgpt.com/docs/developer-commands) confirma o enfileiramento por sessão, sem comprovar por si só conexão ao aplicativo atual.

`codex app-server daemon version`, com acesso local fora do sandbox, terminou com código 1:

```text
failed to connect to %USERPROFILE%\.codex\app-server-control\app-server-control.sock
Nenhuma conexão pôde ser feita porque a máquina de destino as recusou ativamente. (os error 10061)
```

O app apresenta a sessão atual ativa, mas o endpoint de controle padrão consultado não aceita conexão. Esse resultado não demonstra suporte a retorno nem ausência de outros canais. Abrir um daemon separado para carregar a sessão atual poderia introduzir outro dono; isso não foi usado como substituto.

## Critérios de T01

| Evidência | Estado |
| --- | --- |
| Identificação explícita da sessão original | Confirmada |
| Comando queue disponível/documentado | Confirmado |
| Canal padrão acessível a esse chat | Falhou: conexão recusada |
| Worker AGY e resultado estruturado | Parcial: JSON válido; timeout, sem resposta útil |
| Callback enfileirado | Não tentado; canal indisponível |
| Novo turno na mesma sessão | Não comprovado |
| Processamento e confirmação | Não comprovados |

Não extrapolar o suporte de CLIs/sessões próprias para a UI em uso. A próxima decisão dependerá do relatório deste spike.

## Resultado do ensaio real

Instrumento: `scripts/spikes/t01_worker.py`. Uma chamada real, sem repetição.

- Início: 19/10/2026 10:33:31 BRT; encerramento do processo: 10:34:43 BRT (aproximadamente 73 segundos, incluindo startup).
- Evento `init`, seguido de um `result`; nenhuma linha JSON inválida. Modelo efetivo não informado no trecho de init inspecionado; perfil solicitado foi o padrão existente.
- Conversation ID AGY: `267226e8-c6ae-4932-a214-d65fbef02ba9`.
- CLI exit code: 0; `result.status`: `SUCCESS`; `response`: string vazia; `num_turns`: 0.
- stderr: `[agy] print timeout after 1m0s with turn in progress; returning partial output`.
- Os contadores retornaram zero, mas isso não comprova ausência de consumo no provedor: não houve resultado final útil, e o backend do turno não foi reconciliado.
- Veredito do ensaio: **não aprovado**. Timeout e resposta vazia prevalecem sobre o exit code e a etiqueta SUCCESS. A versão do instrumento foi ajustada para também expor o timeout e retornar falha ao chamador; não houve nova chamada de modelo.

Saídas brutas e relatório estruturado em `work/spikes/t01/`, ignorados pelo Git. O processo CLI terminou; o estado final do trabalho no backend AGY não foi comprovado. Não repetir ou retomar silenciosamente essa conversa.

## Canal do aplicativo

Inspeção somente leitura dos processos confirmou um `codex.exe` app-server pertencente à instalação do aplicativo, sem listener anunciado nos argumentos. Isso não prova que não existam outros canais; apenas não forneceu um endpoint externo utilizável. Nenhum token, argumento completo ou credencial foi exposto.

## Conclusão e próximo passo

T01 produziu o relatório de viabilidade, mas o aceite de retomada não passou. T02 e o motor completo permanecem sem implementação.

Antes de outro ensaio pago: localizar uma integração suportada para entrega à sessão nativa atual (ou obter decisão explícita sobre iniciar uma sessão gerenciada pela ponte). Para AGY, investigar startup/perfil e o timeout por meios locais/documentados antes de autorizar nova tentativa com limites revistos. Usar a conversa registrada ao reconciliar, em vez de selecionar a mais recente ou disparar um worker duplicado.

## Pesquisa posterior — 19/10/2026

- [Changelog oficial AGY](https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md): documenta retorno parcial com exit 0 e aviso em stderr ao expirar print-timeout. Explica o envelope do teste; não identifica a causa da demora. Não basta aumentar o limite ou aceitar SUCCESS.
- [Issue AGY #508](https://github.com/google-antigravity/antigravity-cli/issues/508): descreve problemas históricos Windows/headless/stdin e casos em que erros de quota apareciam como timeout. Colaborador informa correção em v1.1.1; a versão local é 1.4.0. Não atribuir automaticamente o problema atual àquele bug antigo. Nosso wrapper herda stdin; fechar explicitamente stdin em print de um turno é uma hipótese de correção a verificar, não uma solução comprovada.
- [Headless oficial AGY](https://www.agy.dev/docs/cli/headless/): recomenda protocolo persistente com stdin/stdout stream-json e evento user explícito; cada turno termina com result. Essa é a interface candidata para a ponte orientada a eventos. A documentação e o changelog divergem quanto a exit codes em timeout; manter validação de resposta, stderr e resultado.
- [Issue Codex #37403, comentário de contribuidor em 02/09/2026](https://github.com/openai/codex/issues/37403): informa que Desktop não compartilha o daemon da CLI e usa instância própria do app-server. Relato originalmente macOS; compatível com o sintoma local, sem provar todo comportamento Windows atual. Não iniciar outro writer na sessão em uso.

Sequência proposta, sem novas inferências executadas: (1) analisar logs locais do AGY no horário e conversa específicos, com remoção de segredos; (2) avaliar stdin explicitamente fechado ou protocolo stream-json com entrada explícita, uma hipótese por teste; (3) manter permissões, modelo e limites registrados; (4) investigar canal suportado do app antes de insistir em queue ou mudar para sessão própria. Nenhuma atualização, mudança de configuração ou desativação de sandbox realizada nesta pesquisa.

## Nova tentativa autorizada — 19/10/2026

Antes do retry, a leitura do log da primeira conversa esclareceu que a autenticação silenciosa funcionou (linha 164), o backend recebeu o perfil `Gemini 3.8 Flash (High)` (linhas 160/172/175) e o MCP `open-design` continuava conectando após 30s e 60s (linhas 215–216). As mensagens iniciais de login ausente não representam o estado final da autenticação. A espera do MCP é uma hipótese mais específica para investigar; não foi provada como única causa.

Usuário autorizou tentar novamente. Ensaio executado de 10:42:43 a 10:43:58 BRT, com uma mensagem `user` por stdin stream-json, entrada fechada após envio, mesmo sandbox, modo plan, perfil padrão e limite interno de 60 segundos. Nenhuma configuração global ou servidor MCP foi alterado.

Resultado: novamente eventos init/result, JSON válido, `SUCCESS` e CLI exit 0, mas resposta vazia e timeout registrado no stderr. O instrumento devolveu exit 1 e `worker_succeeded=false`, como esperado para falha real. Conversation AGY `9a733531-7fc4-4d34-92ec-83bfb989df8c`; evidências em `work/spikes/t01-stream/`.

Mudar apenas o transporte de entrada não resolveu o teste. Não houve callback Codex nem repetição adicional. Próxima investigação: carregamento MCP/perfil por execução e reconciliação das conversas registradas; suporte para excluir MCP apenas por execução ainda não encontrado na ajuda consultada. Não desabilitar conectores globalmente por efeito colateral deste teste.

## Ensaio com configuração MCP local — 19/10/2026

Novo pedido do usuário: testar a proposta anexada, usando tlc-spec-driven. Escopo mantido em uma resposta exata, um turno, modo plan/sandbox, perfil padrão, 60s internos e 75s externos; nenhuma tarefa de negócio ou ferramenta autorizada ao worker. Este é um teste de worker headless; não é o fluxo completo com retorno por evento.

A [documentação MCP AGY](https://www.agy.dev/docs/mcp/) descreve configuração por workspace em `.agents/mcp_config.json` e o campo `disabled`. O instrumento ganhou `--isolate-mcp`: cria apenas na pasta temporária entradas desabilitadas para browsermcp, context7, open-design, playwright e shadcn. A precedência sobre a configuração global não estava comprovada e foi tratada como hipótese.

A primeira invocação local falhou ao criar a pasta `.agents` no sandbox, antes de iniciar AGY; não houve inferência nessa invocação. A execução com permissão apropriada iniciou exatamente um worker. Nenhuma configuração global foi escrita.

- Chat coordenador desta execução: `01a15543-4df3-78b3-853e-3dd07a139b28`, obtido de `CODEX_THREAD_ID`; distinto do chat dos dois ensaios anteriores.
- Horário: 14:45:50–14:47:03 BRT, cerca de 73s.
- Conversation AGY: `17c851de-1c08-4514-820a-e8387b9f1f2d`.
- Log `~/.gemini/antigravity-cli/cli.log`: autenticação silenciosa bem-sucedida (linha 142); perfil backend `Gemini 3.8 Flash (High)` (linhas 138/150/154). Isso comprova configuração observada, não execução útil do modelo.
- Linha 193: quatro servidores ainda conectando após 30s: browsermcp, context7, open-design e playwright. O isolamento pretendido não foi observado; não foi um teste efetivamente sem MCP. Não atribuir o resultado apenas ao open-design nem concluir que MCP é a única causa.
- Resultado: eventos init/result válidos, CLI exit 0 e SUCCESS, mas timeout no stderr, resposta vazia e zero turnos reportados. Instrumento exit 1, `worker_succeeded=false`.
- Evidências: `work/spikes/t01-workspace-mcp/result.json`, `worker.stdout.jsonl` e `worker.stderr.log`. O campo callback_status dessa captura herdou o diagnóstico do daemon anterior; não houve nova consulta ao daemon nesta execução. O instrumento foi corrigido depois para registrar simplesmente que callback não foi tentado.
- Os contadores zero não certificam ausência de consumo. Processo CLI encerrado; estado final do turno backend não reconciliado. Sem continuação ou retry automático.

**Veredito: não aprovado.** Três chamadas reais ao todo; nenhuma entrega ao Codex. Próximo passo técnico: verificar carregamento e precedência da configuração local na versão 1.4.0 e reconciliar as conversas antes de outro ensaio. Alteração global de MCP não está incluída neste teste. Gate de retorno nativo segue separado e sem comprovação; o núcleo Go permanece não implementado.

## Auditoria da configuração efetiva — 19/10/2026

Investigação solicitada pelo usuário após o terceiro ensaio. Nenhuma nova chamada de modelo, início de conversa ou execução de ferramenta MCP. Instrumento reproduzível: `python scripts/spikes/mcp_config_audit.py`. Executado com exit 0 às 14:50:51 BRT. Saída sanitizada em `work/spikes/mcp-config/audit.json`; não contém argumentos, variáveis ou credenciais dos servidores.

### O que está comprovado

- O arquivo `~/.gemini/config/mcp_config.json` contém quatro servidores: browsermcp, context7, open-design e playwright. Todos habilitados (`disabled` ausente ou false). A CLI `agy mcp list` exibe exatamente esses quatro.
- A listagem na raiz do projeto e na fixture `work/spikes/mcp-config/workspace` é idêntica, com exit 0 em ambos os casos. A fixture tem `.agents/mcp_config.json` desabilitando os quatro e adicionando uma entrada sentinela desabilitada. A listagem não inclui a sentinela e mantém os quatro habilitados. Esse subcomando inspeciona configuração de usuário, conforme o changelog; isoladamente não prova como o backend combina configurações.
- A documentação textual embutida no executável local descreve fontes globais e de plugins; não menciona fonte workspace nessa seção. Trecho extraído em `work/spikes/mcp-config/embedded-mcp-location.txt`. A string literal `.agents/mcp_config.json` também não foi encontrada no binário; ausência de literal não prova ausência de implementação.
- O ensaio headless anterior é a evidência de runtime pertinente: apesar do arquivo local de desativação, os quatro MCPs foram conectados; após 60s apenas open-design continuava pendente (log `cli-20261019_144551.log`, linhas 193–194). Não foi demonstrada uma regra de precedência local sobre global.
- O hash SHA-256 do arquivo global permaneceu idêntico antes/depois da auditoria: `c2f531866a23e048096ce1ba0521a0ae337bca0796bde681f934841f78011b12`. Executável auditado: SHA-256 `4f6c7a807f3fae54bc61e74b8469ac79d07702ade5c62de62041bba44e2a7be0`.

### Documentação versus versão instalada

A [página oficial MCP](https://antigravity.google/docs/mcp) descreve configuração por workspace, mas não estabelece a precedência usada no nosso ensaio. O texto embutido na versão instalada difere dessa página; não extrapolar a documentação atual para suporte comprovado no AGY 1.4.0 local.

O [changelog oficial](https://github.com/google-antigravity/antigravity-cli/blob/main/CHANGELOG.md), seção 1.1.9, explica que execuções headless/one-shot continuam aguardando a inicialização MCP para receber o conjunto completo de ferramentas. A seção 1.1.16 descreve os subcomandos `mcp` como gerenciamento da configuração de usuário. Esse comportamento é compatível com a espera observada. Login silencioso funcionou; os logs não provam que o modelo chegou a produzir uma resposta ou que a cota estava disponível.

### Conclusão operacional

O override workspace usado pelo spike **não é um mecanismo de isolamento validado nesta instalação**. Não repetir `--isolate-mcp` esperando um teste sem conectores. A espera por open-design é um bloqueio de inicialização observado; sua remoção ainda não foi testada e não garante resolver todos os problemas.

Para um próximo ensaio isolado, primeiro é necessário um mecanismo efetivo: uma opção por execução documentada e validada, ou uma alteração temporária explícita na configuração global com restauração verificada. Esta auditoria não alterou a configuração global nem executou esse ensaio. Retorno nativo ao chat e reconciliação do backend permanecem pendentes; total de chamadas reais continua três.

## Decisões arquiteturais e operacionais aprovadas — 21/10/2026

Análise cruzada realizada no Antigravity IDE com inspeção da documentação builtin local (`%USERPROFILE%/.gemini/antigravity/builtin/skills/agy-customizations/docs/mcp_servers.md`) e do arquivo global `~/.gemini/config/mcp_config.json`, seguida de aprovação explícita do usuário:

1. **Confirmação de escopo MCP no AGY 1.4.0 e causa do bloqueio no `open-design`:**
   - A documentação interna embutida na instalação confirma que `mcp_config.json` no AGY 1.4.0 possui apenas dois locais suportados: **Global** (`~/.gemini/config/mcp_config.json`) e **Plugin** (`plugins/<plugin_name>/mcp_config.json`). Descoberta por workspace (`.agents/`) aplica-se a Rules e Skills, não a servidores MCP nesta versão.
   - Em `~/.gemini/config/mcp_config.json`, o conector `open-design` invoca `Open Design.exe` (`daemon-cli.mjs mcp`) apontando para o named pipe `\\.\pipe\open-design-sidecar-689d26b6adde8c7504f3c8aee9e47e57`. Quando o aplicativo desktop Open Design não está em execução, a tentativa de conexão/bootstrap bloqueia a inicialização headless do `agy.exe` além da janela de 60s.

2. **Decisão 1 — Auto-Skip do `open-design` na Ponte Go e no Script do Worker (Zero alteração permanente):**
   - Antes de invocar `agy.exe`, o script do spike (`scripts/spikes/t01_worker.py`) e o futuro adaptador Go (`internal/providers/antigravity`) devem verificar em `< 1ms` se o pipe `\\.\pipe\open-design-sidecar-...` (ou o processo `Open Design.exe`) está ativo no Windows.
   - Se o Open Design **não** estiver aberto, desabilitar temporariamente `open-design` em `~/.gemini/config/mcp_config.json` (`"disabled": true`), garantindo restauração atômica do conteúdo original em bloco `finally` com verificação de hash SHA-256 antes e depois da execução.

3. **Decisão 2 — Arquitetura de Retorno Bidirecional (`Codex ⇄ Antigravity`) sem Orca (R06/R08/R14):**
   - Como o aplicativo **Codex Desktop** isola seu `app-server` e não expõe socket externo (`app-server-control.sock`) para `codex queue` após o encerramento do turno, fica aprovado o **modo de retorno síncrono bloqueante no processo da ponte** para hosts sem canal externo de wakeup pós-turno:
     - A maestra (Codex Desktop ou Antigravity IDE) invoca a ponte Go via comando de terminal e mantém a chamada aguardando o término do processo da ponte no nível do sistema operacional (**zero chamadas de inferência ou polling de LLM durante a espera**).
     - Ao término do worker (`agy.exe` ou `codex exec`), a ponte persiste o evento no diário/outbox e devolve o resultado estruturado no `stdout` do comando para consolidação imediata pela maestra no mesmo turno.
     - No sentido inverso (**Antigravity IDE como maestra**), além do modo síncrono, a ferramenta nativa de execução em background já desperta o chat automaticamente via `<SYSTEM_MESSAGE>` quando o processo da ponte conclui.
   - Com isso, a delegação bidirecional (`Codex -> Antigravity` e `Antigravity -> Codex`) fica aprovada sem dependência do Orca.

4. **Decisão 3 — Reconciliação das conversas anteriores e autorização de novo ensaio T01:**
   - As três conversas AGY dos spikes anteriores com timeout (`267226e8-c6ae-4932-a214-d65fbef02ba9`, `9a733531-7fc4-4d34-92ec-83bfb989df8c` e `17c851de-1c08-4514-820a-e8387b9f1f2d`) estão **oficialmente descartadas/encerradas**, sem `resume`.
   - Está **autorizada 1 nova execução real curta** de `scripts/spikes/t01_worker.py` implementando o auto-skip do `open-design` (1 turno, modo `plan`/`sandbox`, timeout interno 60s / externo 75s, 0 retries automáticos, perfil padrão da CLI, resposta exata esperada `MYO_T01_WORKER_OK`). Passando esse ensaio e confirmada a restauração por SHA-256 do `mcp_config.json`, o Gate T01 fica aprovado para avançar para T02.

5. **Decisão 4 — Toolchain Go:**
   - Instalação do Go autorizada pelo usuário (instalador oficial `Go 1.27.0` `go1.27.0.windows-amd64.msi` baixado via `winget`, com conclusão manual pelo usuário para liberar `T02–T09`).

## Quarto ensaio real (`--auto-skip-open-design`) e descoberta de `sharedfragment.go:175` — 21/10/2026

Com autorização explícita do usuário, foi executada 1 chamada de `scripts/spikes/t01_worker.py` com `--auto-skip-open-design` às 16:42:42–16:43:49 BRT (conversa AGY `28b3437a-f915-4247-b4a0-528e613e3290`, descartada sem `resume`). O arquivo global `~/.gemini/config/mcp_config.json` foi integralmente restaurado no bloco `finally` e permanece com hash SHA-256 original (`c2f531866a23e048096ce1ba0521a0ae337bca0796bde681f934841f78011b12`).

### O que o log (`~/.gemini/antigravity-cli/cli.log`) comprovou

1. **A chave `"disabled": true` em `~/.gemini/config/mcp_config.json` não impediu a conexão no runtime do Language Server (v1.4.2), e outros dois MCPs também bloquearam:**
   - Embora o pipe configurado `open-design-sidecar-689d26b6adde8c7504f3c8aee9e47e57` não estivesse ativo (apenas três pipes órfãos antigos estavam presentes em `\\.\pipe`) e o script tenha gravado `"disabled": true` para `open-design` em `~/.gemini/config/mcp_config.json`, o `mcp_manager.go` ainda tentou conectar os 4 servidores:
     - Linha 199 (`16:43:13`): `MCP: 4 server(s) still connecting after 30s: browsermcp, context7, open-design, playwright`
     - Linha 200 (`16:43:43`): `MCP: 3 server(s) still connecting after 1m0s: browsermcp, context7, open-design` (apenas `playwright` concluiu entre 30s e 60s).
   - Além disso, a varredura em `~/.gemini` revelou que existem múltiplos arquivos de configuração por superfície (`~/.gemini/config/mcp_config.json`, `~/.gemini/antigravity/mcp_config.json` e `~/.gemini/antigravity-ide/mcp_config.json`).
2. **Causa-raiz exata do bloqueio antes da resposta do LLM (`sharedfragment.go:175`):**
   - Linha 206 (`16:43:48.856`, no exato milissegundo em que o `print timeout after 1m0s` encerrou a execução):
     ```text
     E1021 16:43:48.856667 590 sharedfragment.go:175] Failed to append MCP servers from specs: context canceled
     ```
   - Isso comprova que, ao montar o fragmento de prompt de sistema (`sharedfragment.go:175`), o Language Server aguarda que **todos** os servidores presentes em `"mcpServers"` terminem de conectar (`browsermcp`, `context7` e `open-design` ficaram pendentes até 60s).
   - Como `"disabled": true` é ignorado pelo carregador do Language Server (ou mesclado de `~/.gemini/antigravity/mcp_config.json`), a única forma de um worker headless (que não utiliza MCPs) não bloquear em `sharedfragment.go:175` é remover temporariamente as entradas de `"mcpServers"` (`{"mcpServers": {}}`) nos arquivos lidos pelo CLI (`~/.gemini/config/mcp_config.json` e `~/.gemini/antigravity/mcp_config.json`) durante a chamada e restaurar os bytes originais em `finally` com verificação SHA-256.
3. **Correção aplicada no instrumento (`t01_worker.py`) e centralização de `mcp_config.json`:**
   - No Windows, `tempfile.TemporaryDirectory` levantou `PermissionError: [WinError 32]` no `__exit__` porque o processo filho do Language Server reteve o handle do diretório por alguns milissegundos após o término do `agy.exe`. Corrigido com `ignore_cleanup_errors=True` em `t01_worker.py`.
   - Com autorização explícita do usuário ("centralize"), as duas duplicatas legadas (`~/.gemini/antigravity/mcp_config.json` e `~/.gemini/antigravity-ide/mcp_config.json`) foram movidas para `~/.gemini/antigravity-backup/` (`antigravity_mcp_config.json.bak` e `antigravity_ide_mcp_config.json.bak`), mantendo **apenas `~/.gemini/config/mcp_config.json`** como arquivo ativo unificado.

## Quinto ensaio real (configuração MCP centralizada + skip temporário de `mcpServers`) — APROVADO em 21/10/2026

Com a configuração centralizada em `~/.gemini/config/mcp_config.json` e autorização do usuário ("pode ordar o testes"), `scripts/spikes/t01_worker.py` foi executado com `--input-mode stream --auto-skip-open-design`, esvaziando temporariamente `"mcpServers": {}` durante a chamada headless e restaurando os bytes originais em `finally`:

- **Probe ID:** `0622e640-63a1-4029-be93-4c0b76756413`
- **Horário:** 21/10/2026, `20:19:19.949Z` a `20:19:33.777Z` (~13,8s totais de processo; **`duration_seconds`: `5.346s`** no turno do modelo).
- **Conversation ID AGY:** `b2ffec5f-bc52-4990-8657-031faa8ba2a9`
- **Eventos JSONL recebidos:** `["init", "step_update", "step_update", "step_update", "result"]` (`invalid_json_lines: 0`)
- **Status e Resposta:** `status: "SUCCESS"`, `response: "MYO_T01_WORKER_OK\n"`, `num_turns: 1`, `timeout_reported: false`, **`worker_succeeded: true`** (`exit_code: 0`).
- **Contadores de uso expostos pelo AGY:** `input_tokens: 9084`, `output_tokens: 295`, `thinking_tokens: 284`, `cache_read_tokens: 0`, `total_tokens: 9379`.
- **Integridade da configuração global:**
  - `global_mcp_sha256_before`: `c2f531866a23e048096ce1ba0521a0ae337bca0796bde681f934841f78011b12`
  - `global_mcp_sha256_after`: `c2f531866a23e048096ce1ba0521a0ae337bca0796bde681f934841f78011b12`
  - `global_mcp_restored_verified`: **`true`**
- **Evidências salvas em:** `work/spikes/t01-centralized-skip/result.json`, `worker.stdout.jsonl` e `worker.stderr.log`.
- **Veredito final da T01:** **APROVADO.** Gate T01 concluído; liberado o início de `T02–T09` assim que o compilador Go estiver no `PATH`.
