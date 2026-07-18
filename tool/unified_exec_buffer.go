package tool

const unifiedExecOutputMaxBytes = 1024 * 1024

type unifiedExecHeadTailBuffer struct {
	maxBytes     int
	headBudget   int
	tailBudget   int
	head         []byte
	tail         []byte
	omittedBytes int
}

func newUnifiedExecHeadTailBuffer(maxBytes int) *unifiedExecHeadTailBuffer {
	if maxBytes < 0 {
		maxBytes = 0
	}
	headBudget := maxBytes / 2
	return &unifiedExecHeadTailBuffer{
		maxBytes:   maxBytes,
		headBudget: headBudget,
		tailBudget: maxBytes - headBudget,
	}
}

func (b *unifiedExecHeadTailBuffer) Write(data []byte) (int, error) {
	if b == nil || len(data) == 0 {
		return len(data), nil
	}
	originalLen := len(data)
	if b.maxBytes == 0 {
		b.omittedBytes += len(data)
		return originalLen, nil
	}
	if len(b.head) < b.headBudget {
		remaining := b.headBudget - len(b.head)
		if len(data) <= remaining {
			b.head = append(b.head, data...)
			return originalLen, nil
		}
		b.head = append(b.head, data[:remaining]...)
		data = data[remaining:]
	}
	b.pushTail(data)
	return originalLen, nil
}

func (b *unifiedExecHeadTailBuffer) Drain() ([]byte, int) {
	if b == nil {
		return nil, 0
	}
	out := make([]byte, 0, len(b.head)+len(b.tail))
	out = append(out, b.head...)
	out = append(out, b.tail...)
	omitted := b.omittedBytes
	b.head = nil
	b.tail = nil
	b.omittedBytes = 0
	return out, omitted
}

func (b *unifiedExecHeadTailBuffer) Snapshot() ([]byte, int) {
	if b == nil {
		return nil, 0
	}
	out := make([]byte, 0, len(b.head)+len(b.tail))
	out = append(out, b.head...)
	out = append(out, b.tail...)
	return out, b.omittedBytes
}

func (b *unifiedExecHeadTailBuffer) pushTail(data []byte) {
	if len(data) == 0 {
		return
	}
	if b.tailBudget == 0 {
		b.omittedBytes += len(data)
		return
	}
	if len(data) >= b.tailBudget {
		b.omittedBytes += len(b.tail) + len(data) - b.tailBudget
		b.tail = append(b.tail[:0], data[len(data)-b.tailBudget:]...)
		return
	}
	b.tail = append(b.tail, data...)
	if excess := len(b.tail) - b.tailBudget; excess > 0 {
		b.omittedBytes += excess
		copy(b.tail, b.tail[excess:])
		b.tail = b.tail[:len(b.tail)-excess]
	}
}
