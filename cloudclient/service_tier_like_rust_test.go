package cloudclient

import (
	"bytes"
	"google.golang.org/protobuf/encoding/protowire"
	"testing"
)

func TestCloudServiceTierWireLikeRust(t *testing.T) {
	fast := "ultrafast"
	for _, tc := range []struct {
		set   bool
		value *string
		want  []byte
	}{
		{false, nil, nil}, {true, nil, []byte{18, 0}}, {true, &fast, append([]byte{10, 9}, []byte(fast)...)},
	} {
		request := AddUserMessageRequest{ThreadID: "t", ClientUserMessageID: "m", Input: []UserMessageInput{{Kind: UserMessageText, Text: "hi"}}, Mode: SubmissionStartIfIdle, ServiceTierSet: tc.set, ServiceTier: tc.value}
		data := encodeAddUserMessageRequest(request)
		var got []byte
		for len(data) > 0 {
			num, typ, n := protowire.ConsumeTag(data)
			if n < 0 {
				t.Fatal("invalid tag")
			}
			data = data[n:]
			if num == 20 {
				got, n = protowire.ConsumeBytes(data)
			} else {
				n = protowire.ConsumeFieldValue(num, typ, data)
			}
			if n < 0 {
				t.Fatal("invalid field")
			}
			data = data[n:]
		}
		if !bytes.Equal(got, tc.want) {
			t.Errorf("tier wire=%x want=%x", got, tc.want)
		}
		request.Mode = SubmissionQueue
		valid := validAddUserMessageRequest(request)
		if valid == tc.set {
			t.Errorf("queue tier set=%v valid=%v", tc.set, valid)
		}
	}
}
