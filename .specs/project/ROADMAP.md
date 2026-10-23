# Roadmap

Cada marco termina com uma demonstração observável.

**Status em 23/10/2026:** M0–M2 concluídos. M3 parcial: Antigravity como maestra comprovado; adaptador Copilot implementado como experimental (não validado por falta de acesso a modelos explícitos na CLI); Claude pendente. Primeira versão pública (v0.1) cobre apenas o par Codex ⇄ Antigravity.

| Marco | Entrega | Critério para avançar |
| --- | --- | --- |
| M0 — viabilidade do chat de origem | Identificação do host e sessão, sinais de conclusão, teste de retorno no Codex e AGY | Mesma sessão recebe um resultado e executa novo turno; UI observada se declarada suportada |
| M1 — núcleo Go simulado | Execuções, mensagens, fila persistida, deduplicação, desligamento | Simulações de sucesso, duplicata, crash, entrega ambígua e pergunta pendente preservam estados; zero inferência nos testes locais |
| M2 — primeiro par real | Codex maestra + Antigravity worker, skill com comandos estáveis, relatório final | Worker conclui; maestra retorna uma vez; ponte fecha após finalização; uso registrado quando exposto |
| M3 — papéis invertidos e demais CLIs | Antigravity maestra; adaptadores Claude e Copilot | Cada provedor demonstra maestra e worker; modelos e capacidades registrados; teste separado de cada UI |
| M4 — primeira versão funcional | Entrada por agentes locais suportados, planejamento com grill-me, retomada explícita | Matriz de plataformas publicada com suportado/parcial/não validado; nenhum host sem teste declarado suportado |
| Posterior | Edição delimitada, múltiplos workers, integrações opcionais | Decisão específica de permissões e necessidade real |

## Primeiro ensaio real

Tarefa sem dados de negócio: um worker analisa uma pequena nota de requisitos preparada para teste e devolve uma conclusão curta. A maestra deve esperar por um evento, conferir a conclusão e finalizar. O ensaio prova o mecanismo, não a qualidade de auditorias completas.

Não chamar todos os modelos de uma vez. Testar cada adaptador com o menor fluxo útil; a matriz de papéis pode ser verificada com pares selecionados, sem exigir todas as combinações possíveis em toda mudança.
