// Package model — описание графика: тип, подписи и ряды точек.
package model

import "time"

// типы графиков
const (
	TypeLine = "line" // ряды во времени: нагрузка, ошибки, задержки за период
	TypeBar  = "bar"  // сравнение по категориям: сервисы, поды, ошибки по сервисам
)

// Spec — что нарисовать. Unit — единица значений (rps, ratio, seconds, bytes, cores,
// count или своя подпись): по ней значения переводятся в привычный вид (МБ, %, мс).
type Spec struct {
	Type   string
	Title  string
	Unit   string
	Series []Series
}

// Series — ряд точек (линия или столбцы одного цвета) с подписью в легенде.
type Series struct {
	Name   string
	Points []Point
}

// Point — точка ряда: Time — у линии, Label — у столбца.
type Point struct {
	Time  time.Time
	Label string
	Value float64
}
