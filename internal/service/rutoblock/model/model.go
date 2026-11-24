// Package model — контрактные модели клиента чёрного списка ruto.
package model

import (
	"errors"
	"time"
)

// ErrRefused — ruto отказал по своим правилам (400/403): внутренняя сеть, белый список, слишком
// широкая сеть или долгий срок, сеть недавно разблокировал человек, исчерпан лимит агентов.
var ErrRefused = errors.New("ruto refused the block")

// RefusedError — отказ ruto; Message — его текст словами, можно показать человеку как есть.
type RefusedError struct {
	Message string
}

func (e *RefusedError) Error() string {
	return ErrRefused.Error() + ": " + e.Message
}

func (e *RefusedError) Unwrap() error {
	return ErrRefused
}

// BlockReq — заблокировать адрес или сеть на Duration (ruto пускает агента от 1m до 7 суток).
type BlockReq struct {
	Ip       string
	Reason   string
	Duration time.Duration
}

// Block — блок в ruto. ExpiresAt нулевое — без срока (так блокирует только человек).
// Source admin — блок поставил или подтвердил человек: агент его не меняет, ruto вернул как есть.
type Block struct {
	Id        string
	Ip        string
	Source    string
	Active    bool
	ExpiresAt time.Time
}
