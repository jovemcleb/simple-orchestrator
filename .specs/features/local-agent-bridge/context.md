# Contexto da entrevista

## Confirmado pelo usuário em 16/10/2026

1. Entrada preferida: chat gráfico existente; também deve poder iniciar pelas CLIs de Codex, Claude, Copilot e Antigravity.
2. Limite inicial: análise e relatórios, antes de edição de código.
3. Validação: simulações e teste real curto entre duas CLIs.

## Decisões propostas de implementação

- Uma ponte Go por execução ativa, iniciada sob demanda; sem um serviço global permanente inicialmente.
- Uma maestra por execução, qualquer um dos quatro provedores quando seu adaptador estiver validado.
- Começar a prova com Codex + AGY porque os contratos de mensagens/sinais já foram encontrados, sem fixar essa dupla no produto.
- Comunicação por interfaces estruturadas e ID explícito; uma UI sem retorno comprovado não é tratada como equivalente à CLI.
- Relatórios curtos com evidências locais; catálogo de modelos por provedor, sem fallback silencioso.

Essas escolhas organizam o plano. A entrevista seguinte só precisa tratar decisões que mudem a implementação; não repetir as três respostas acima.
