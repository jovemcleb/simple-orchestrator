# Simple Orchestrator

## Visão

Permitir que as IAs locais do usuário cooperem a partir de suas próprias CLIs e interfaces gráficas, preservando a escolha de modelos, com uma ponte Go iniciada sob demanda e orientada a eventos.

## Problema

A coordenação atual depende do Orca. Há preocupação com chamadas e contexto desnecessários. Uma skill pode definir a cooperação, mas precisa de transporte, identificação de sessão e um mecanismo verificável de retorno. Espera sem inferência não elimina o custo de processar contexto no turno seguinte.

## Objetivos mensuráveis

- Provar primeiro um ciclo: maestra delega, encerra turno, worker conclui, mesma sessão da maestra recebe e processa o resultado.
- Zero chamadas de inferência para verificar repetidamente se há novidade durante espera.
- Suportar os quatro provedores como maestra ou worker ao concluir a primeira versão funcional; liberar por adaptador e capacidade comprovada.
- Registrar modelo solicitado e efetivamente observado, IDs, tentativas, estados e caminhos de evidências.
- Encerrar a ponte ao concluir o fluxo sem descartar tarefas, respostas ou entregas ambíguas.

## Decisões confirmadas

- Go para a ponte; Windows primeiro.
- Independente das funções de orquestração do Orca.
- Entrada preferida: CLI ou chat gráfico do próprio agente. Interface própria não é requisito inicial.
- Qualquer uma das quatro IAs pode coordenar; uma única maestra é dona de cada execução.
- Primeiros fluxos apenas de análise e relatórios locais.
- Simulações e teste real curto antes de declarar o comportamento validado.

## Stack proposta

- Go: `go1.27.0 windows/amd64` confirmado em `C:/Program Files/Go/bin/go.exe` em 21/10/2026.
- Núcleo: biblioteca padrão, processos filhos com pipes, protocolo JSON e fila persistida em arquivos locais com um escritor.
- Sem banco ou serviço externo obrigatório no primeiro corte.
- SDK oficial Go do Copilot é uma opção para seu adaptador, sujeito à escolha de versão e compatibilidade; não é dependência do núcleo.
- Skill portátil de operação planejada para depois de estabilizar os comandos; referências sob demanda.

## Limites iniciais

Sem edição de produto, banco, planilha ou deploy. Relatórios e artefatos locais são permitidos. Credenciais continuam sob gestão das CLIs. Não prometer que modelos, login, cotas, ferramentas ou conectores de uma UI estão disponíveis em uma sessão headless.

Fora do primeiro corte: interface web própria, serviço Windows permanente, broker externo, execução distribuída e delegação recursiva irrestrita. Interface gráfica já existente continua sendo o alvo de integração, com gates por host.
