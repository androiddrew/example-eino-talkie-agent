package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"time"

	talkie "github.com/androiddrew/talkie-core"
)

// latency joins talkie's events with the agent's timings, by reply, and logs
// one turn_latency line per reply. It also prints talkie's state changes as
// a status line.
//
// talkie calls OnEvent on its audio goroutine and the agent reports from
// Respond's, so both send records over a buffered channel to one goroutine
// that joins them. Neither ever blocks: when the channel is full, the record
// is dropped and counted.
type latency struct {
	records chan latencyRecord
	dropped atomic.Int64
	join    *latencyJoin
}

// latencyRecord is a talkie event or an agent timing.
type latencyRecord struct {
	event  *talkie.Event
	timing *agentTiming
}

func newLatency(log *slog.Logger, status io.Writer) *latency {
	return &latency{records: make(chan latencyRecord, 256), join: newLatencyJoin(log, status)}
}

// OnEvent is talkie.Config.OnEvent.
func (l *latency) OnEvent(ev talkie.Event) {
	l.send(latencyRecord{event: &ev})
}

// Timed is Agent.timed.
func (l *latency) Timed(t agentTiming) {
	l.send(latencyRecord{timing: &t})
}

func (l *latency) send(r latencyRecord) {
	select {
	case l.records <- r:
	default:
		l.dropped.Add(1)
	}
}

// Run joins records until ctx is done.
func (l *latency) Run(ctx context.Context) {
	sweep := time.NewTicker(10 * time.Second)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-l.records:
			l.join.dropped = l.dropped.Load()
			l.join.add(r)
		case now := <-sweep.C:
			l.join.sweep(now)
		}
	}
}

// latencyJoin is the joining goroutine's state.
type latencyJoin struct {
	log     *slog.Logger
	status  io.Writer // nil prints no status
	dropped int64     // records dropped so far, for the log
	replies map[string]*replyLatency
	// transcripts waits for the agent to start the reply each one answers:
	// transcripts have no reply ID, so they are joined by session and text.
	transcripts []transcriptAt
	// thinking holds interruptions of a reply that hadn't played, by
	// session: they carry no reply ID, and come before the agent returns.
	thinking map[string]interruptAt
}

type interruptAt struct {
	by talkie.InterruptedBy
	at time.Time
}

type transcriptAt struct {
	session, text string
	at            time.Time
	stt           time.Duration
}

// replyLatency is what is known of one reply.
type replyLatency struct {
	session, text string
	transcript    *transcriptAt
	agent         *agentTiming
	firstAudio    time.Time // EventReplyStarted
	end           time.Time // EventReplyFinished or EventReplyStopped
	interruptedBy talkie.InterruptedBy
	seen          time.Time // when the first record arrived, for the sweep
}

func newLatencyJoin(log *slog.Logger, status io.Writer) *latencyJoin {
	return &latencyJoin{log: log, status: status, replies: map[string]*replyLatency{}, thinking: map[string]interruptAt{}}
}

func (j *latencyJoin) add(r latencyRecord) {
	if t := r.timing; t != nil {
		rep := j.reply(t.Reply, r.timing.Done)
		rep.agent, rep.session, rep.text = t, t.Session, t.Text
		if rep.transcript == nil {
			rep.transcript = j.takeTranscript(t.Session, t.Text)
		}
		if in, ok := j.thinking[t.Session]; ok && rep.firstAudio.IsZero() && !in.at.Before(t.Respond) {
			rep.interruptedBy = in.by
			delete(j.thinking, t.Session)
		}
		j.maybeLog(t.Reply, rep)
		return
	}
	ev := r.event
	switch ev.Type {
	case talkie.EventState:
		if j.status != nil {
			fmt.Fprintf(j.status, "[%s]\n", ev.State)
		}
	case talkie.EventTranscript:
		tr := transcriptAt{session: ev.Session, text: ev.Text, at: ev.Time, stt: ev.STT}
		for id, rep := range j.replies { // the agent reported first
			if rep.agent != nil && rep.transcript == nil && rep.session == tr.session && rep.text == tr.text {
				rep.transcript = &tr
				j.maybeLog(id, rep)
				return
			}
		}
		j.transcripts = append(j.transcripts, tr)
	case talkie.EventReplyStarted:
		if ev.Reply != "" {
			j.reply(ev.Reply, ev.Time).firstAudio = ev.Time
		}
	case talkie.EventReplyFinished, talkie.EventReplyStopped:
		if ev.Reply != "" {
			rep := j.reply(ev.Reply, ev.Time)
			rep.end = ev.Time
			j.maybeLog(ev.Reply, rep)
		}
	case talkie.EventInterrupted:
		if ev.Reply == "" { // while thinking: the agent's timing will claim it
			j.thinking[ev.Session] = interruptAt{by: ev.InterruptedBy, at: ev.Time}
			return
		}
		rep := j.reply(ev.Reply, ev.Time)
		rep.interruptedBy = ev.InterruptedBy
		j.maybeLog(ev.Reply, rep)
	}
}

func (j *latencyJoin) reply(id string, at time.Time) *replyLatency {
	rep, ok := j.replies[id]
	if !ok {
		rep = &replyLatency{seen: at}
		j.replies[id] = rep
	}
	return rep
}

func (j *latencyJoin) takeTranscript(session, text string) *transcriptAt {
	for i, tr := range j.transcripts {
		if tr.session == session && tr.text == text {
			j.transcripts = append(j.transcripts[:i], j.transcripts[i+1:]...)
			return &tr
		}
	}
	return nil
}

// maybeLog logs the reply once its transcript is known, the agent has
// returned and its playback has ended, or it was interrupted before it
// played. A reply whose transcript was dropped waits for the sweep.
func (j *latencyJoin) maybeLog(id string, rep *replyLatency) {
	if rep.agent == nil || rep.transcript == nil {
		return
	}
	if rep.end.IsZero() && (rep.interruptedBy == "" || !rep.firstAudio.IsZero()) {
		return
	}
	j.emit(id, rep)
}

// sweep logs replies that will get no more events, such as one that wrote
// nothing to play, and forgets transcripts no reply answered.
func (j *latencyJoin) sweep(now time.Time) {
	const stale = time.Minute
	for id, rep := range j.replies {
		if now.Sub(rep.seen) > stale {
			if rep.agent != nil {
				j.emit(id, rep)
			} else {
				delete(j.replies, id)
			}
		}
	}
	kept := j.transcripts[:0]
	for _, tr := range j.transcripts {
		if now.Sub(tr.at) <= stale {
			kept = append(kept, tr)
		}
	}
	j.transcripts = kept
	for session, in := range j.thinking {
		if now.Sub(in.at) > stale {
			delete(j.thinking, session)
		}
	}
}

func (j *latencyJoin) emit(id string, rep *replyLatency) {
	delete(j.replies, id)
	a := rep.agent
	attrs := []any{"reply", id, "session", rep.session}
	since := func(name string, from, to time.Time) {
		if !from.IsZero() && !to.IsZero() {
			attrs = append(attrs, name, to.Sub(from).Round(time.Millisecond))
		}
	}
	if tr := rep.transcript; tr != nil {
		attrs = append(attrs, "stt", tr.stt.Round(time.Millisecond))
	}
	since("first_token", a.Respond, a.FirstToken)
	since("first_sentence", a.Respond, a.FirstSentence)
	since("first_audio", a.FirstToken, rep.firstAudio)
	if tr := rep.transcript; tr != nil {
		since("end_to_end", tr.at.Add(-tr.stt), rep.firstAudio)
	}
	end := rep.end
	if end.IsZero() {
		end = a.Done
	}
	attrs = append(attrs, "chars", a.Chars)
	since("total", a.Respond, end)
	if rep.interruptedBy != "" {
		attrs = append(attrs, "interrupted_by", rep.interruptedBy)
	}
	if a.Err != nil && a.Err != context.Canceled {
		attrs = append(attrs, "err", a.Err)
	}
	if j.dropped > 0 {
		attrs = append(attrs, "dropped_records", j.dropped)
	}
	j.log.Info("turn_latency", attrs...)
}
