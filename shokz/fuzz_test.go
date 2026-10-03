package shokz

import (
	"slices"
	"testing"
)

// FuzzDecode feeds random bytes to the frame reader. It must never panic and
// must always make progress through the input.
func FuzzDecode(f *testing.F) {
	for _, seed := range [][]byte{replyGetOn, replyGetOff, replySet, frameGetDolby, append(replySet, replyGetOn...)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for len(data) > 0 {
			frame, n, err := Next(data)
			if err != nil {
				return
			}
			if n == 0 {
				t.Fatal("Next returned a frame without consuming bytes")
			}
			if m, err := Decode(frame); err == nil {
				m.DolbyState()
			}
			data = data[n:]
		}
	})
}

// FuzzClient lets the fake earbuds answer with random bytes. Whatever they
// send, the client must stop, start with a get, and send at most one set.
func FuzzClient(f *testing.F) {
	f.Add(slices.Concat(replyGetOff, replySet, replyGetOn), uint8(0), true)
	f.Add(slices.Concat(replyGetOn, replySet, replyGetOff), uint8(5), false)
	f.Add(replyGetOff, uint8(1), true)
	f.Fuzz(func(t *testing.T, data []byte, chunk uint8, on bool) {
		fake := &fakeEarbuds{
			pending: data,
			chunk:   int(chunk),
			respond: func(*fakeEarbuds, []byte) []byte { return nil },
		}
		NewClient(fake).Set(on)
		reqs := fake.requests()
		if len(reqs) == 0 || reqs[0] != GetDolby {
			t.Fatalf("sent %v, want a get first", reqs)
		}
		sets := 0
		for _, r := range reqs {
			switch r {
			case SetOn, SetOff:
				sets++
			case GetDolby:
			default:
				t.Fatalf("sent an unknown frame: %v", reqs)
			}
		}
		if sets > 1 {
			t.Fatalf("sent %d set requests: %v", sets, reqs)
		}
	})
}
