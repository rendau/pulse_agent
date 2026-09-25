package model

import "time"

// Turn — пара «вопрос — итоговый ответ» в беседе. Промежуточные вызовы инструментов
// в историю не попадают: ответ pulse весит до 100 KB, и каждый следующий вопрос
// тащил бы их за собой.
type Turn struct {
	Question string
	Answer   string
	At       time.Time
}
