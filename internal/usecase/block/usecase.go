// Package block — решение админа по уведомлению security (кнопки «Заблокировать на 24 ч / 7 дней»,
// «Не блокировать» под уведомлением в боте): заблокировать адрес атаки в API-шлюзе ruto или нет.
// Агент сам ничего не блокирует — только по нажатию админа. Одно итоговое решение на
// уведомление: после успешной блокировки повторные нажатия (из любого чата) возвращают её и
// ничего не вызывают. «Не блокировать» и отказ ruto — не итоговые.
package block

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/samber/lo"

	blockModel "github.com/rendau/pulse_agent/internal/domain/block/model"
	notifyModel "github.com/rendau/pulse_agent/internal/domain/notify/model"
	"github.com/rendau/pulse_agent/internal/errs"
	rutoblockModel "github.com/rendau/pulse_agent/internal/service/rutoblock/model"
	"github.com/rendau/pulse_agent/internal/usecase/block/model"
)

// maxReason — потолок причины блокировки в ruto, символов
const maxReason = 500

// durations — сроки, которые можно выбрать
var durations = map[string]time.Duration{model.Duration24h: 24 * time.Hour, model.Duration7d: 7 * 24 * time.Hour}

type Usecase struct {
	notify NotifyServiceI
	block  BlockServiceI
	ruto   RutoI
	locks  *keyedLock
}

func New(notify NotifyServiceI, block BlockServiceI, ruto RutoI) *Usecase {
	return &Usecase{notify: notify, block: block, ruto: ruto, locks: &keyedLock{locks: map[int64]*keyLock{}}}
}

// Decide — решение админа по уведомлению. Ошибки: errs.InvalidRequest — неверные поля, уведомление
// не security или его ключ — не адрес; errs.ObjectNotFound — нет уведомления; errs.ServiceNA —
// ruto недоступен, ответил 5xx или не принял ключ. Отказ ruto по его правилам — не ошибка:
// Decision.Refused.
func (u *Usecase) Decide(ctx context.Context, req *model.Request) (*model.Result, error) {
	if err := validate(req); err != nil {
		return nil, err
	}
	n, err := u.notify.Notification(ctx, req.NotificationId)
	if err != nil {
		return nil, fmt.Errorf("notify.Notification: %w", err)
	}
	if n.Kind != notifyModel.KindSecurity {
		return nil, fmt.Errorf("%w: notification %d is %s, only security notifications can be blocked", errs.InvalidRequest, n.Id, n.Kind)
	}
	if !validAddr(n.Key) {
		return nil, fmt.Errorf("%w: notification %d key %q is not an ip or network", errs.InvalidRequest, n.Id, n.Key)
	}

	// одно решение по уведомлению за раз: второй админ ждёт первого и получает его итог, а не
	// второй вызов ruto (одна реплика; между репликами одно итоговое решение держит уникальный индекс)
	unlock, err := u.locks.lock(ctx, n.Id)
	if err != nil {
		return nil, err
	}
	defer unlock()

	decisions, err := u.block.Decisions(ctx, n.Id)
	if err != nil {
		return nil, fmt.Errorf("block.Decisions: %w", err)
	}
	if final := blockModel.Final(decisions); final != nil {
		return &model.Result{Decision: final, Repeat: true}, nil
	}

	d := &blockModel.Decision{
		NotificationId: n.Id, Action: req.Action, Duration: req.Duration, Ip: n.Key,
		ById: req.User.Id, ByName: req.User.Name, Client: req.Client, ConversationId: req.ConversationId,
	}
	if req.Action == blockModel.ActionSkip {
		if skip := blockModel.FirstSkip(decisions); skip != nil {
			return &model.Result{Decision: skip, Repeat: true}, nil
		}
		return u.record(ctx, d)
	}

	// бот закрыл соединение — блокировка в ruto всё равно доводится и записывается
	ctx = context.WithoutCancel(ctx)
	b, err := u.ruto.Block(ctx, &rutoblockModel.BlockReq{Ip: n.Key, Reason: reason(n, req.User), Duration: durations[req.Duration]})
	if refused, ok := errors.AsType[*rutoblockModel.RefusedError](err); ok {
		d.Refused = lo.CoalesceOrEmpty(strings.TrimSpace(refused.Message), "ruto refused the block")
		return u.record(ctx, d)
	}
	if err != nil {
		return nil, fmt.Errorf("ruto.Block: %w", err)
	}
	d.Ip, d.RutoBlockId, d.RutoSource = lo.CoalesceOrEmpty(b.Ip, n.Key), b.Id, b.Source
	if !b.ExpiresAt.IsZero() {
		d.Until = new(b.ExpiresAt)
	}
	return u.record(ctx, d)
}

// record — сохранить решение; уже было итоговое (записано, пока шёл вызов, другой репликой) — оно.
func (u *Usecase) record(ctx context.Context, d *blockModel.Decision) (*model.Result, error) {
	saved, created, err := u.block.Record(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("block.Record: %w", err)
	}
	return &model.Result{Decision: saved, Repeat: !created}, nil
}

func validate(req *model.Request) error {
	req.ConversationId = strings.TrimSpace(req.ConversationId)
	req.Action, req.Duration = strings.TrimSpace(req.Action), strings.TrimSpace(req.Duration)
	req.User.Id, req.User.Name = strings.TrimSpace(req.User.Id), strings.TrimSpace(req.User.Name)
	switch {
	case req.NotificationId <= 0:
		return fmt.Errorf("%w: notification id must be positive", errs.InvalidRequest)
	case req.ConversationId == "":
		return fmt.Errorf("%w: conversation_id is required", errs.InvalidRequest)
	case req.User.Id == "" && req.User.Name == "":
		return fmt.Errorf("%w: user is required (who made the decision)", errs.InvalidRequest)
	case req.Action == blockModel.ActionSkip && req.Duration != "":
		return fmt.Errorf("%w: duration is only for action block", errs.InvalidRequest)
	case req.Action == blockModel.ActionSkip:
		return nil
	case req.Action != blockModel.ActionBlock:
		return fmt.Errorf("%w: action %q; expected block or skip", errs.InvalidRequest, req.Action)
	}
	if _, ok := durations[req.Duration]; !ok {
		return fmt.Errorf("%w: duration %q; expected %s or %s", errs.InvalidRequest, req.Duration, model.Duration24h, model.Duration7d)
	}
	return nil
}

// reason — причина блокировки в ruto: created_by там — владелец ключа (pulse), поэтому админ,
// который решил, — в тексте. Длиннее maxReason — обрезается заголовок.
func reason(n *notifyModel.Notification, user model.User) string {
	who := user.Name
	switch {
	case who == "":
		who = user.Id
	case user.Id != "":
		who += " (" + user.Id + ")"
	}
	prefix, suffix := "pulse: ", " — решение в Telegram: "+who
	title := strings.Join(strings.Fields(n.Title), " ")
	room := maxReason - utf8.RuneCountInString(prefix) - utf8.RuneCountInString(suffix)
	return truncate(prefix+truncate(title, room)+suffix, maxReason)
}

// truncate — не длиннее max символов, обрезанное — с «…».
func truncate(s string, max int) string {
	runes := []rune(s)
	switch {
	case len(runes) <= max:
		return s
	case max <= 0:
		return ""
	}
	return string(runes[:max-1]) + "…"
}

// validAddr — IPv4/IPv6 или подсеть.
func validAddr(addr string) bool {
	if _, err := netip.ParseAddr(addr); err == nil {
		return true
	}
	_, err := netip.ParsePrefix(addr)
	return err == nil
}

// keyedLock — замок на уведомление: решения по одному уведомлению — по одному за раз, по разным —
// параллельно; ждущий уходит по отмене контекста.
type keyedLock struct {
	mu    sync.Mutex
	locks map[int64]*keyLock
}

type keyLock struct {
	ch   chan struct{}
	refs int
}

func (l *keyedLock) lock(ctx context.Context, key int64) (func(), error) {
	l.mu.Lock()
	k := l.locks[key]
	if k == nil {
		k = &keyLock{ch: make(chan struct{}, 1)}
		l.locks[key] = k
	}
	k.refs++
	l.mu.Unlock()

	select {
	case k.ch <- struct{}{}:
		return func() {
			<-k.ch
			l.release(key, k)
		}, nil
	case <-ctx.Done():
		l.release(key, k)
		return nil, fmt.Errorf("wait for decision on notification %d: %w", key, ctx.Err())
	}
}

func (l *keyedLock) release(key int64, k *keyLock) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if k.refs--; k.refs == 0 {
		delete(l.locks, key)
	}
}
