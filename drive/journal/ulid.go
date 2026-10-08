package journal

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"sync"
	"time"
)

// Crockford base32 alphabet used by ULID.
const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var (
	ulidMu       sync.Mutex
	ulidLastMs   int64
	ulidLastRand = new(big.Int)
	randMax      = new(big.Int).Lsh(big.NewInt(1), 80)
)

// NewULID returns a 26-character ULID for the given time. Within one
// millisecond the random part increases monotonically, so ids from one
// process sort in creation order; across processes the millisecond prefix
// orders them.
func NewULID(t time.Time) string {
	ms := t.UnixMilli()
	ulidMu.Lock()
	defer ulidMu.Unlock()
	var r *big.Int
	if ms == ulidLastMs && ulidLastRand.Sign() != 0 {
		r = new(big.Int).Add(ulidLastRand, big.NewInt(1))
		if r.Cmp(randMax) >= 0 {
			// Overflow of the 80-bit part is practically impossible; wait a
			// millisecond instead of wrapping around.
			ms++
			r = randomBig()
		}
	} else {
		r = randomBig()
	}
	ulidLastMs = ms
	ulidLastRand = r
	return encodeTime(ms) + encodeRandom(r)
}

func randomBig() *big.Int {
	var buf [10]byte
	if _, err := rand.Read(buf[:]); err != nil {
		panic("journal: crypto/rand failed: " + err.Error())
	}
	return new(big.Int).SetBytes(buf[:])
}

func encodeTime(ms int64) string {
	var out [10]byte
	v := uint64(ms)
	for i := 9; i >= 0; i-- {
		out[i] = alphabet[v&31]
		v >>= 5
	}
	return string(out[:])
}

func encodeRandom(r *big.Int) string {
	var out [16]byte
	v := new(big.Int).Set(r)
	mod := new(big.Int)
	thirtyTwo := big.NewInt(32)
	for i := 15; i >= 0; i-- {
		v.DivMod(v, thirtyTwo, mod)
		out[i] = alphabet[mod.Int64()]
	}
	return string(out[:])
}

// ULIDTime decodes the millisecond timestamp of a ULID.
func ULIDTime(id string) (time.Time, error) {
	if len(id) != 26 {
		return time.Time{}, errors.New("journal: ulid must be 26 characters")
	}
	var ms uint64
	for i := 0; i < 10; i++ {
		idx := strings.IndexByte(alphabet, id[i])
		if idx < 0 {
			return time.Time{}, errors.New("journal: ulid has an invalid character")
		}
		ms = ms<<5 | uint64(idx)
	}
	return time.UnixMilli(int64(ms)).UTC(), nil
}
