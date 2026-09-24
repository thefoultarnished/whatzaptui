package backend

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// stallAfter is how long an operation may run before the watchdog reports
// it as stalled. Var, not const, so tests can shrink it.
var stallAfter = 15 * time.Second

// stackDumpMinGap rate-limits goroutine dumps so a stuck backend writes one
// dump per minute at most instead of one per stalled request.
var stackDumpMinGap = time.Minute

// stallWatch reports a long-running operation that stops making progress.
// Every stallAfter it logs "<op>.stall" with the current phase, and the
// first time it fires it writes a goroutine dump next to the action log so
// a hang (deadlock, stuck DB connection) can be pinpointed without a
// debugger. All methods are safe on a nil receiver.
type stallWatch struct {
	phase    atomic.Value // string
	done     chan struct{}
	stopOnce sync.Once
}

func (a *App) startStallWatch(op string) *stallWatch {
	w := &stallWatch{done: make(chan struct{})}
	w.phase.Store("start")
	start := time.Now()
	go func() {
		ticker := time.NewTicker(stallAfter)
		defer ticker.Stop()
		dumped := false
		for {
			select {
			case <-w.done:
				return
			case <-ticker.C:
				phase, _ := w.phase.Load().(string)
				a.actionLog.Event(op+".stall", map[string]string{
					"phase":     phase,
					"elapsedMs": durMs(time.Since(start)),
				})
				if !dumped {
					dumped = true
					a.dumpGoroutines(op + ":" + phase)
				}
			}
		}
	}()
	return w
}

// Phase records the step the watched operation is in, reported on stall.
func (w *stallWatch) Phase(p string) {
	if w != nil {
		w.phase.Store(p)
	}
}

// Stop ends the watch. Idempotent.
func (w *stallWatch) Stop() {
	if w != nil {
		w.stopOnce.Do(func() { close(w.done) })
	}
}

var lastStackDump atomic.Int64 // unix nanos of the last dump

// dumpGoroutines writes every goroutine's stack to
// <logs>/stack-<utc>.txt and logs "debug.stackdump" with the file name.
// Stacks hold function names, file:line and raw argument words only; no
// message bodies or tokens. Rate-limited by stackDumpMinGap.
func (a *App) dumpGoroutines(reason string) {
	if a == nil || a.actionLog == nil || a.actionLog.Path() == "" {
		return
	}
	now := time.Now()
	last := lastStackDump.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < stackDumpMinGap {
		return
	}
	if !lastStackDump.CompareAndSwap(last, now.UnixNano()) {
		return
	}
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= 16<<20 {
			buf = buf[:n]
			break
		}
		buf = make([]byte, len(buf)*2)
	}
	path := filepath.Join(filepath.Dir(a.actionLog.Path()), "stack-"+now.UTC().Format("20060102T150405Z")+".txt")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		a.actionLog.Event("debug.stackdump.fail", map[string]string{"reason": reason, "err": truncateErr(err)})
		return
	}
	a.actionLog.Event("debug.stackdump", map[string]string{"reason": reason, "file": filepath.Base(path)})
}
