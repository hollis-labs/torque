package service

import "time"

// SetNowForTest pins the Ops sync clock so backoff can be stepped in tests.
func (o *OpsSync) SetNowForTest(now func() time.Time) { o.now = now }

// SetNowForTest pins the assignment clock.
func (s *AssignmentService) SetNowForTest(now func() time.Time) { s.now = now }
