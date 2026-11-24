package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSubscriptionCovers(t *testing.T) {
	security := &Notification{Kind: KindSecurity, Key: "129.226.55.176", Severity: "critical"}
	alert := &Notification{Kind: KindAlert, Service: "caravan", Severity: "warning"}

	cases := []struct {
		name string
		sub  Subscription
		n    *Notification
		want bool
	}{
		{"всё — security не приходит", Subscription{}, security, false},
		{"любой вид с важностью — security не приходит", Subscription{MinSeverity: "info"}, security, false},
		{"явная подписка на security — приходит", Subscription{Kind: KindSecurity}, security, true},
		{"security с порогом важности — выше порога", Subscription{Kind: KindSecurity, MinSeverity: "warning"}, security, true},
		{"security с порогом важности — ниже порога", Subscription{Kind: KindSecurity, MinSeverity: "critical"},
			&Notification{Kind: KindSecurity, Severity: "warning"}, false},
		{"подписка на алерты — security не приходит", Subscription{Kind: KindAlert}, security, false},
		{"подписка на сервис — security не приходит", Subscription{Service: "caravan"}, security, false},
		{"всё — алерт приходит, как раньше", Subscription{}, alert, true},
		{"сервис — алерт приходит", Subscription{Service: "caravan"}, alert, true},
		{"подписка на security — алерт не приходит", Subscription{Kind: KindSecurity}, alert, false},
		{"другой вид — не приходит", Subscription{Kind: KindDeploy}, alert, false},
		{"важность выше — не приходит", Subscription{MinSeverity: "critical"}, alert, false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, c.sub.Covers(c.n), c.name)
	}
}

func TestKinds(t *testing.T) {
	assert.True(t, KindKnown(KindSecurity), "security можно указать в подписке и приглушении")
	assert.True(t, KindExplicit(KindSecurity))
	for _, kind := range []string{"", KindAlert, KindDeploy, KindLogs, KindSelf, KindPublic} {
		assert.False(t, KindExplicit(kind), kind)
	}
}

func TestMuteCoversSecurity(t *testing.T) {
	security := &Notification{Kind: KindSecurity, Key: "129.226.55.176"}
	assert.True(t, (&Mute{}).Covers(security), "приглушить всё — глушит и security")
	assert.True(t, (&Mute{Kind: KindSecurity, Key: "129.226.55.176"}).Covers(security))
	assert.False(t, (&Mute{Kind: KindSecurity, Key: "1.2.3.4"}).Covers(security), "другой адрес")
}
