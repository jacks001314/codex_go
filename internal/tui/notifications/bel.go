package notifications

const BEL = "\a"

type BELBackend struct{}

func (b *BELBackend) Notify(message string) string {
	return BELPostNotification{}.ANSI()
}

type BELPostNotification struct{}

func (p BELPostNotification) ANSI() string {
	return BEL
}

func (p BELPostNotification) String() string {
	return p.ANSI()
}

func BELNotification(enabled bool) string {
	if enabled {
		return BEL
	}
	return ""
}
