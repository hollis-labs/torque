package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hollis-labs/libs/mesh/eventbus"
	"github.com/hollis-labs/libs/mesh/eventbus/relay"
	"github.com/hollis-labs/torque/internal/hubrelay"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
)

// startHubRelay ships the state-event outbox to the event hub. The hub is found
// through the environment (HOLLIS_EVENTS_URL and the app's token file, as in
// mesh/eventbus); with no URL there is no hub and nothing starts. It logs and
// carries on when it cannot start: shipping events is never a reason to take
// the daemon down.
//
// The relay reads the outbox, which is durable, and moves its cursor (kept in
// event_outbox_cursors, per outbox generation) only over events the hub settled
// or the client durably quarantined. A hub that is down, or a missing or
// under-scoped credential, holds events back and loses none. The client's spool
// and quarantine live under <state dir>/events-hub.
func startHubRelay(ctx context.Context, wg *sync.WaitGroup, store *sqlstore.Store, stateDir string) {
	meta, err := store.OutboxMeta(ctx)
	if err != nil {
		log.Printf("[hub] outbox meta: %v", err)
		return
	}
	dir := filepath.Join(stateDir, "events-hub")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[hub] state dir: %v", err)
		return
	}
	pub, err := eventbus.FromEnv("torque", eventbus.Config{
		SpoolDir: dir,
		OnError:  func(err error) { log.Printf("[hub] client: %v", err) },
	})
	if err != nil {
		log.Printf("[hub] client: %v", err)
		return
	}
	deliverer, ok := pub.(relay.Deliverer)
	if !ok {
		log.Printf("[hub] relay is on but %s is not set: nothing to send to", eventbus.EnvURL)
		return
	}
	r, err := relay.New(relay.Config{
		Source: hubrelay.Source{Store: store, OnGap: func(from, to int64) {
			log.Printf("[hub] outbox events %d..%d were trimmed before they were sent; resuming after %d", from, to, to)
		}},
		Cursor:    hubrelay.Cursor{Store: store, Consumer: hubrelay.Consumer(meta.Generation)},
		Deliverer: deliverer,
		OnError:   func(err error) { log.Printf("[hub] relay: %v", err) },
	})
	if err != nil {
		log.Printf("[hub] relay: %v", err)
		return
	}
	log.Printf("[hub] relaying the event outbox (generation %d) to the hub", meta.Generation)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.Run(ctx); err != nil && ctx.Err() == nil {
			log.Printf("[hub] relay stopped: %v", err)
		}
		// ctx is already done at shutdown; the last delivery attempt needs its own.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := pub.Close(closeCtx); err != nil {
			log.Printf("[hub] close: %v", err)
		}
	}()
}
