package service

import "time"

// SetNowForTest pins the Ops sync clock so backoff can be stepped in tests.
func (o *OpsSync) SetNowForTest(now func() time.Time) { o.now = now }
