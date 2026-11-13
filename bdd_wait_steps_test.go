// MIT License
//
// Copyright (C) 2025 John Kleijn
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE

package alog_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cucumber/godog"
)

// waitTimeout is how long a scenario waits for something that should happen.
const waitTimeout = 5 * time.Second

// waiting is a ScanWait that is running in the background.
type waiting struct {
	cancel context.CancelFunc
	done   chan struct{}
	data   string
	ok     bool
}

func (w *world) startWait() error {
	s, err := w.currentScanner()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	wt := &waiting{cancel: cancel, done: make(chan struct{})}
	w.wait = wt

	go func() {
		defer close(wt.done)

		if wt.ok = s.ScanWait(ctx); wt.ok {
			wt.data = string(s.Data())
		}
	}()

	// the scan can not have returned, as there was nothing to read
	select {
	case <-wt.done:
		if wt.ok {
			return nil
		}

		return errors.New("scan stopped immediately")
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func (w *world) finishWait() (*waiting, error) {
	if w.wait == nil {
		return nil, errors.New("no scan is waiting")
	}

	select {
	case <-w.wait.done:
		return w.wait, nil
	case <-time.After(waitTimeout):
		return nil, errors.New("the waiting scan did not return")
	}
}

func (w *world) registerWait(sc *godog.ScenarioContext) {
	sc.Step(`^a scan is waiting for a record$`, w.startWait)

	sc.Step(`^I cancel the wait$`, func() error {
		if w.wait == nil {
			return errors.New("no scan is waiting")
		}
		w.wait.cancel()
		return nil
	})

	sc.Step(`^the waiting scan yields "([^"]*)"$`, func(want string) error {
		wt, err := w.finishWait()
		if err != nil {
			return err
		}
		if !wt.ok || wt.data != want {
			return fmt.Errorf("waiting scan returned %q (record: %t), want %q", wt.data, wt.ok, want)
		}
		return nil
	})

	sc.Step(`^the waiting scan stops without a record$`, func() error {
		wt, err := w.finishWait()
		if err != nil {
			return err
		}
		if wt.ok {
			return fmt.Errorf("waiting scan returned %q", wt.data)
		}
		w.err = w.scanner.Err()
		if w.err != nil {
			return fmt.Errorf("the scanner failed: %w", w.err)
		}
		return nil
	})

	sc.Step(`^the waiting scan stops with (\w+)$`, func(name string) error {
		wt, err := w.finishWait()
		if err != nil {
			return err
		}
		if wt.ok {
			return fmt.Errorf("waiting scan returned %q", wt.data)
		}
		w.err = w.scanner.Err()
		return w.expectError(name)
	})
}
