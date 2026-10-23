# Simple Orchestrator — orientação aos agentes

Ponte local em Go que delega tarefas entre CLIs de agentes (hoje Codex ⇄ Antigravity). Antes de mudar comportamento, leia `.specs/project/STATE.md`, a spec em `.specs/features/local-agent-bridge/` e o guia `CONTRIBUTING.md`.

## Regras do projeto

- Preserve os critérios de aceite e as decisões de `.specs/`. Exemplos genéricos das skills não são requisitos do produto.
- Sucesso exige prova (R07): código de saída zero ou texto parcial não bastam. Cada provedor precisa de um sinal verificável de fim de turno.
- Sem substituição silenciosa de modelo (R04): se a CLI trocar o modelo pedido, a tarefa falha com motivo.
- Zero inferência durante a espera (R06): a ponte espera por processo, arquivo ou evento, nunca chamando modelos por timer.
- Testes nunca abrem janelas reais nem chamam CLIs de verdade: use os lançadores, sondas e workers falsos já existentes.
- Gates: `gofmt -l .` vazio, `go vet ./...` e `go test ./...` passando. Ensaios reais são curtos, de baixo consumo e registrados em `docs/validation/`.
- Prefira a biblioteca padrão do Go. Ferramentas citadas nas skills (testify, goleak, errgroup, golangci-lint etc.) são opções a avaliar, não dependências automáticas.
- Dimensione raciocínio e agentes pela tarefa; não lance auditorias para mudanças pequenas.

## Skills de desenvolvimento

Leia somente a skill aplicável, antes de usá-la:

| Tarefa | Skill |
| --- | --- |
| Goroutines, canais, cancelamento, espera e shutdown em Go | `.agents/skills/go-concurrency-patterns/SKILL.md` |
| Testes Go de entrega, duplicatas, falhas e recuperação | `.agents/skills/golang-testing/SKILL.md` |
| Desenhar a arquitetura (opcional, não distribuída) | `.agents/skills/desenha-sistema/SKILL.md`, se você a tiver localmente |

Origem, licenças e hashes das skills de terceiros estão em `.agents/skills/sources.json`; preserve a atribuição e registre adaptações. A skill do produto, usada pelos hosts para delegar, é `skills/local-agent-flow/SKILL.md`.
