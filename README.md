# pulse_agent

Агент над [pulse](https://github.com/rendau/pulse): вопрос об инфраструктуре → разбор
LLM с инструментами pulse → ответ текстом и/или по полям (JSON), с графиками. Один агент для
всех систем: Telegram-бот, service-desk, разбор алертов.

- API для систем-клиентов — [docs/agent-api.md](docs/agent-api.md).
- Эталонные вопросы — `make eval` (`evals/cases.yaml`).
- Руководство по коду — [CLAUDE.md](CLAUDE.md).
