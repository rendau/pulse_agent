package constant

// EndpointTool — инструмент pulse, вызывающий диагностические ручки сервисов.
const EndpointTool = "call_service_endpoint"

// AudienceHuman — ответ ручки только для человека: модели не показывается.
const AudienceHuman = "human"

// MaxHumanReplies — ответов ручек для человека на один ответ.
const MaxHumanReplies = 5

// HumanReplySent — что видит модель вместо ответа ручки для человека (%d — HTTP-статус ручки).
const HumanReplySent = `{"audience":"human","sent_to_human":true,"status_code":%d,"note":"Ответ ручки отправлен человеку напрямую, как есть; тебе он недоступен. Не пересказывай и не додумывай его содержимое — одной фразой скажи, что отправил ответ (сервис и ручка)."}`

// HumanReplyLimit — ответ модели сверх лимита ответов ручек для человека.
const HumanReplyLimit = "не отправлен: на ответ не больше %d ответов ручек для человека"
