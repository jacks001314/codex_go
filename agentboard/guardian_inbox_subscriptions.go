package agentboard

import (
	"errors"
	"sort"
	"strings"
)

// SetSubscription subscribes or unsubscribes this live reviewer inbox to a
// channel (Rust #63357 ActorInbox::set_subscription). An exact channel name can
// be subscribed to before the channel exists; only the name is validated. The
// queued evidence already selected for delivery is preserved by an unsubscribe,
// and a closed inbox refuses the change.
func (i *GuardianActorInbox) SetSubscription(channel string, subscribe bool) error {
	if i == nil {
		return errors.New("Guardian inbox is unavailable")
	}
	channel = strings.TrimSpace(channel)
	if subscribe {
		if err := validateChannelName(channel); err != nil {
			return err
		}
	}
	i.mu.Lock()
	if i.closed {
		i.mu.Unlock()
		return errors.New("Guardian inbox is closed")
	}
	index := sort.SearchStrings(i.channels, channel)
	present := index < len(i.channels) && i.channels[index] == channel
	switch {
	case subscribe && !present:
		i.channels = append(i.channels, "")
		copy(i.channels[index+1:], i.channels[index:])
		i.channels[index] = channel
	case !subscribe && present:
		i.channels = append(i.channels[:index], i.channels[index+1:]...)
	default:
		i.mu.Unlock()
		return nil
	}
	i.mu.Unlock()
	if subscribe {
		i.delivery.addSubscriber(channel, i)
	} else {
		i.delivery.removeSubscriber(channel, i)
	}
	return nil
}

// SubscribedChannels lists the channels this live reviewer selected, in sorted
// order.
func (i *GuardianActorInbox) SubscribedChannels() []string {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]string(nil), i.channels...)
}

// addSubscriber indexes one live inbox under a channel.
func (d *GuardianLiveDelivery) addSubscriber(channel string, inbox *GuardianActorInbox) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	if d.subscribers == nil {
		d.subscribers = map[string]map[*GuardianActorInbox]struct{}{}
	}
	members := d.subscribers[channel]
	if members == nil {
		members = map[*GuardianActorInbox]struct{}{}
		d.subscribers[channel] = members
	}
	members[inbox] = struct{}{}
}

// removeSubscriber drops one live inbox from a channel index.
func (d *GuardianLiveDelivery) removeSubscriber(channel string, inbox *GuardianActorInbox) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.removeSubscriberLocked(channel, inbox)
}

func (d *GuardianLiveDelivery) removeSubscriberLocked(channel string, inbox *GuardianActorInbox) {
	members := d.subscribers[channel]
	if members == nil {
		return
	}
	delete(members, inbox)
	if len(members) == 0 {
		delete(d.subscribers, channel)
	}
}

// unsubscribeAll drops a closed inbox from every channel index.
func (d *GuardianLiveDelivery) unsubscribeAll(inbox *GuardianActorInbox) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for channel := range d.subscribers {
		d.removeSubscriberLocked(channel, inbox)
	}
}

// subscribersFor snapshots the live inboxes subscribed to a channel.
func (d *GuardianLiveDelivery) subscribersFor(channel string) []*GuardianActorInbox {
	d.mu.Lock()
	defer d.mu.Unlock()
	members := d.subscribers[channel]
	inboxes := make([]*GuardianActorInbox, 0, len(members))
	for inbox := range members {
		inboxes = append(inboxes, inbox)
	}
	return inboxes
}

// PublishByChannel queues one committed original for every live reviewer inbox
// subscribed to any of the channels that carried it, once per inbox even when
// several channels match (Rust #63357). posts maps each matched channel to the
// post that stored the original there. The returned count is the number of
// inboxes that received the original.
func (d *GuardianLiveDelivery) PublishByChannel(id GuardianEvidenceID, posts map[string]string) (int, error) {
	if d == nil {
		return 0, errors.New("Guardian delivery is unavailable")
	}
	if d.Closed() {
		return 0, errors.New("Guardian delivery is closed")
	}
	channels := make([]string, 0, len(posts))
	for channel := range posts {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	delivered := map[*GuardianActorInbox]struct{}{}
	for _, channel := range channels {
		for _, inbox := range d.subscribersFor(channel) {
			if _, ok := delivered[inbox]; ok {
				continue
			}
			if err := inbox.publish(id, posts[channel]); err != nil {
				return len(delivered), err
			}
			delivered[inbox] = struct{}{}
		}
	}
	return len(delivered), nil
}
