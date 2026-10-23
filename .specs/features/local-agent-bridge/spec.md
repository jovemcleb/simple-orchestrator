# Spec — Ponte local entre agentes

Status em 21/10/2026: implementações e testes de T01–T08 estão presentes; o aceite de restauração Go em T06 tem uma lacuna aberta. T09–T12 pendentes. Esta spec continua sendo o contrato de aceitação: concluir tarefas de implementação não comprova, por si só, suporte real a cada CLI/UI ou o fluxo ponta a ponta. Consulte `.specs/project/STATE.md` e `tasks.md` para o estado corrente.

## Histórias P1

Como usuário, inicio um fluxo no agente local escolhido, atribuo modelos e papéis, recebo a conclusão no destino combinado e encerro sem manter IAs verificando progresso. Cada história abaixo pode ser demonstrada em um fluxo pequeno.

| ID | Requisito e aceite observável |
| --- | --- |
| R01 | Quando o agente chamar a ponte com um plano válido, iniciar sob demanda e devolver ID persistido da execução; duas chamadas com a mesma chave não criam dois fluxos. |
| R02 | Quando uma CLI ou UI tiver ferramentas de shell/MCP autorizadas, permitir iniciar a ponte por esse canal. Registrar host, provedor, sessão e caminho do workspace; falhar com motivo se não puder determinar um destino seguro. |
| R03 | Quando o usuário selecionar a maestra, preservar esse provedor e perfil; separar capacidades de iniciar tarefa, executar como worker e receber retorno no mesmo chat. O suporte completo exige comprovação para os quatro agentes. |
| R04 | Quando um modelo/esforço for solicitado, usar apenas opções documentadas e registrar solicitado/observado/desconhecido; indisponibilidade não autoriza substituição silenciosa. |
| R05 | Quando a maestra delegar, entregar escopo, restrições, papel e aceite ao worker. Nesta versão, permitir análise e gravação de relatórios apenas nos diretórios autorizados; não executar código/banco de negócio com escrita. |
| R06 | Quando a maestra aguardar trabalho pendente (seja encerrando o turno em hosts com canal de wakeup, seja mantendo a chamada do comando da ponte bloqueada no nível do processo SO), manter a ponte disponível sem chamar modelos por timer. Eventos dos processos/protocolos, espera de processo filho e controles de saúde locais não são inferência. |
| R07 | Quando houver conclusão, falha comprovada ou pergunta bloqueante, persistir o evento antes de encaminhá-lo. Resultado parcial, EOF e saída zero sozinhos não provam sucesso da tarefa. |
| R08 | Quando um retorno chegar ao destino, distinguir persistido, enfileirado, entregue no stdout do comando bloqueante, turno iniciado, processado e confirmado. Declarar retorno concluído quando a mesma sessão maestra receber o payload estruturado (por novo turno observável ou pelo retorno direto da chamada de comando da ponte) e processar o resultado. |
| R09 | Quando um evento duplicado ou atrasado reaparecer, não iniciar duas consolidações nem atualizar a tentativa errada. Entrega ambígua permanece pendente de reconciliação, sem reenvio cego. |
| R10 | Quando todas as tarefas estiverem terminais, não houver perguntas/entregas pendentes e a maestra tiver finalizado, concluir a execução, persistir o estado e encerrar os processos próprios. Uma pausa entre mensagens nunca basta para desligar. |
| R11 | Quando houver interrupção ou parada manual, preservar checkpoints e mensagens pendentes. Ao iniciar novamente, reconciliar antes de retomar. Não prometer recebimento em tempo real enquanto a ponte estiver desligada. |
| R12 | Quando for solicitado relatório de uso, informar chamadas por fase/provedor e contadores expostos pelas CLIs. Cache e raciocínio seguem a semântica de cada provedor; valores ausentes ficam desconhecidos. Não converter tokens em cota da conta sem regra verificada. |
| R13 | Quando o usuário pedir ajuda para montar o fluxo, usar grill-me uma pergunta por vez e gerar um plano pequeno inspirado em orca-flow: objetivo, aceite, papéis, modelos, dependências, permissões e evidências. A maestra faz esse raciocínio; a ponte somente o executa. |
| R14 | Quando o retorno automático à UI não estiver suportado ou testado, informar a limitação e persistir o relatório recuperável; não abrir ou editar outra conversa silenciosamente. CLI headless não certifica integração com UI existente. |
| R15 | Quando houver erro de cota, permissão, autenticação ou modelo, registrar falha/lacuna e encaminhar à maestra/usuário. Repetir só conforme política limitada e autorização existente. |
| R16 | Quando a ponte iniciar um worker, abrir automaticamente um terminal de acompanhamento que exiba ao vivo os eventos legíveis do worker (mensagens, comandos, arquivos, status) e, no fim, o resumo e o status terminal, permanecendo aberto para leitura. O terminal é somente leitura: fechá-lo não interrompe nem altera a tarefa, e a ponte continua dona do processo e do resultado estruturado. Falha ao abrir o terminal é registrada e não bloqueia a delegação. O histórico nas UIs (Codex/Antigravity) segue sincronizado, mas não é o canal de acompanhamento em tempo real. Por padrão (`--watch-native`), a janela é a tela interativa da própria CLI do worker (Codex ou Antigravity), com a tarefa como primeiro prompt; a ponte devolve o resultado quando esse turno termina (validado pelos arquivos de sessão da CLI) e a janela continua aberta, inclusive depois que o comando da maestra termina. Fechar a janela antes do fim do turno interrompe a tarefa. `--watch-native=false` usa o worker headless com o terminal de log somente leitura. |

## Histórias P2

- Workers independentes simultâneos, após validar um worker por vez.
- Perguntas de workers com retomada automática de ida e volta, depois do teste de conclusão básico.
- Integração MCP opcional para hosts em que a chamada por comando não baste.
- Edição de código delimitada por fluxo, com revisão e ownership de arquivos.

## Fora do primeiro corte

Interface própria, coordenação remota, dependência do Orca, troca livre de maestra no meio da execução, instalação automática de modelos/CLIs e alteração direta de bancos de histórico dos aplicativos.

## Cenários de validação

- Um worker termina; a mesma sessão maestra processa o relatório uma vez, mesmo com evento repetido.
- A ponte recebe um novo trabalho enquanto prepara o desligamento: o trabalho é aceito sob o lock ou rejeitado claramente, nunca perdido.
- O worker emite texto final, mas o resultado terminal é falha: não consolidar como sucesso.
- Callback enfileirado sem evidência de processamento: manter entrega pendente, sem afirmar conclusão.
- Reinício durante entrega: reconciliar a outbox com a evidência do destinatário antes de reenviar.
- Modelo inválido, falta de login ou permissão: não substituir perfil ou abrir interface de login automaticamente.
- Pergunta bloqueante ou worker ativo: impedir encerramento automático.

Estratégia autorizada: agentes simulados no desenvolvimento e ensaio real curto. Acompanhamento gráfico e teste de APIs são evidências diferentes.
