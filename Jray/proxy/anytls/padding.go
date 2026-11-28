package anytls

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"strings"

	"github.com/xtls/xray-core/common/errors"
)

const (
	paddingCheckMark = -1
	maxPaddingStop   = 10000
	maxPaddingSize   = 1 << 20
)

var defaultPaddingSchemeRaw = []byte(`stop=8
0=30-30
1=100-400
2=400-500,c,500-1000,c,500-1000,c,500-1000,c,500-1000
3=9-9,500-1000
4=500-1000
5=500-1000
6=500-1000
7=500-1000`)

var defaultPaddingScheme = mustDefaultPaddingScheme()

func mustDefaultPaddingScheme() *paddingScheme {
	p, err := parsePaddingScheme(defaultPaddingSchemeRaw)
	if err != nil {
		panic(err)
	}
	return p
}

type paddingRange struct {
	minSize int
	maxSize int
}

type paddingScheme struct {
	rawScheme []byte
	md5Hex    string
	stop      uint32
	records   map[uint32][]paddingRange
}

func parsePaddingScheme(raw []byte) (*paddingScheme, error) {
	if len(raw) == 0 {
		return nil, errors.New("anytls: empty padding scheme")
	}
	rawCopy := make([]byte, len(raw))
	copy(rawCopy, raw)

	kv := parseSettings(rawCopy)
	if len(kv) == 0 {
		return nil, errors.New("anytls: invalid padding scheme")
	}
	stopStr, ok := kv["stop"]
	if !ok {
		return nil, errors.New("anytls: padding scheme missing stop")
	}
	stopVal, err := strconv.Atoi(strings.TrimSpace(stopStr))
	if err != nil || stopVal < 0 || stopVal > maxPaddingStop {
		return nil, errors.New("anytls: invalid padding stop: ", stopStr)
	}

	sum := md5.Sum(rawCopy)
	ps := &paddingScheme{
		rawScheme: rawCopy,
		md5Hex:    hex.EncodeToString(sum[:]),
		stop:      uint32(stopVal),
		records:   make(map[uint32][]paddingRange),
	}

	for key, value := range kv {
		pkt, parseErr := strconv.ParseUint(key, 10, 32)
		if parseErr != nil || strconv.FormatUint(pkt, 10) != key {
			continue
		}
		var ranges []paddingRange
		for item := range strings.SplitSeq(value, ",") {
			item = strings.TrimSpace(item)
			if item == "c" {
				ranges = append(ranges, paddingRange{minSize: paddingCheckMark})
				continue
			}
			minText, maxText, found := strings.Cut(item, "-")
			if !found {
				continue
			}
			minSize, minErr := strconv.Atoi(strings.TrimSpace(minText))
			if minErr != nil {
				continue
			}
			maxSize, maxErr := strconv.Atoi(strings.TrimSpace(maxText))
			if maxErr != nil {
				continue
			}
			if minSize > maxSize {
				minSize, maxSize = maxSize, minSize
			}
			if minSize <= 0 || maxSize > maxPaddingSize {
				continue
			}
			ranges = append(ranges, paddingRange{minSize: minSize, maxSize: maxSize})
		}
		if len(ranges) > 0 {
			ps.records[uint32(pkt)] = ranges
		}
	}

	return ps, nil
}

// ValidatePaddingScheme checks whether raw is a valid AnyTLS padding scheme.
func ValidatePaddingScheme(raw []byte) error {
	p, err := parsePaddingScheme(raw)
	if err != nil {
		return err
	}
	if len(p.rawScheme) > maxFramePayload {
		return errors.New("anytls: padding scheme exceeds max frame size")
	}
	return nil
}

func (p *paddingScheme) generateRecordPayloadSizes(pkt uint32) []int {
	if p == nil {
		return nil
	}
	ranges := p.records[pkt]
	if len(ranges) == 0 {
		return nil
	}
	sizes := make([]int, 0, len(ranges))
	for _, r := range ranges {
		switch {
		case r.minSize == paddingCheckMark:
			sizes = append(sizes, paddingCheckMark)
		case r.maxSize > r.minSize:
			sizes = append(sizes, randIntRange(r.minSize, r.maxSize))
		default:
			sizes = append(sizes, r.minSize)
		}
	}
	return sizes
}

func (p *paddingScheme) handshakePaddingSize() int {
	sizes := p.generateRecordPayloadSizes(0)
	if len(sizes) == 0 || sizes[0] <= 0 {
		return 0
	}
	if sizes[0] > maxFramePayload {
		return maxFramePayload
	}
	return sizes[0]
}

// randIntRange returns a cryptographically random integer in [minVal, maxVal).
func randIntRange(minVal, maxVal int) int {
	if maxVal <= minVal {
		return minVal
	}
	span := uint64(maxVal - minVal)
	limit := (math.MaxUint64 / span) * span
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			return minVal
		}
		v := binary.LittleEndian.Uint64(b[:])
		if v < limit {
			return minVal + int(v%span)
		}
	}
}
