package execserver

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	clatter "github.com/shurlinet/go-clatter"
	clattercipher "github.com/shurlinet/go-clatter/crypto/cipher"
	clatterdh "github.com/shurlinet/go-clatter/crypto/dh"
	clatterhash "github.com/shurlinet/go-clatter/crypto/hash"
	clatterkem "github.com/shurlinet/go-clatter/crypto/kem"
)

type remoteNoiseIdentity struct {
	dh  clatter.KeyPair
	kem clatter.KeyPair
}

func generateRemoteNoiseIdentity() (*remoteNoiseIdentity, error) {
	suite := remoteNoiseCipherSuite()
	dhKey, err := suite.DH.GenerateKeypair(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate X25519 key: %w", err)
	}
	kemKey, err := suite.SKEM.GenerateKeypair(rand.Reader)
	if err != nil {
		dhKey.Destroy()
		return nil, fmt.Errorf("generate ML-KEM-768 key: %w", err)
	}
	return &remoteNoiseIdentity{dh: dhKey, kem: kemKey}, nil
}

func (i *remoteNoiseIdentity) PublicKey() RemotePublicKey {
	if i == nil {
		return RemotePublicKey{}
	}
	return RemotePublicKey{
		Suite:             noiseChannelSuite,
		X25519PublicKey:   base64.StdEncoding.EncodeToString(i.dh.Public),
		MLKEM768PublicKey: base64.StdEncoding.EncodeToString(i.kem.Public),
	}
}

func (i *remoteNoiseIdentity) Destroy() {
	if i == nil {
		return
	}
	i.dh.Destroy()
	i.kem.Destroy()
}

func remoteNoiseCipherSuite() clatter.CipherSuite {
	return clatter.CipherSuite{
		DH:     clatterdh.NewX25519(),
		EKEM:   clatterkem.NewMlKem768(),
		SKEM:   clatterkem.NewMlKem768(),
		Cipher: clattercipher.NewAesGcm(),
		Hash:   clatterhash.NewSha256(),
	}
}

type remoteNoiseHandshakeObserver struct {
	remoteStaticDH  []byte
	remoteStaticKEM []byte
}

func (o *remoteNoiseHandshakeObserver) OnMessage(event clatter.HandshakeEvent) {
	if len(event.RemoteStaticDH) != 0 {
		o.remoteStaticDH = append(o.remoteStaticDH[:0], event.RemoteStaticDH...)
	}
	if len(event.RemoteStaticKEM) != 0 {
		o.remoteStaticKEM = append(o.remoteStaticKEM[:0], event.RemoteStaticKEM...)
	}
}

func (*remoteNoiseHandshakeObserver) OnError(clatter.HandshakeErrorEvent) {}

type pendingRemoteNoiseHandshake struct {
	handshake *clatter.HybridHandshake
	publicKey RemotePublicKey
}

func readRemoteNoiseHandshake(identity *remoteNoiseIdentity, prologue []byte, request []byte) (*pendingRemoteNoiseHandshake, string, error) {
	if identity == nil {
		return nil, "", errors.New("Noise identity is required")
	}
	observer := &remoteNoiseHandshakeObserver{}
	handshake, err := clatter.NewHybridHandshake(
		clatter.PatternHybridIK,
		false,
		remoteNoiseCipherSuite(),
		clatter.WithStaticKey(identity.dh),
		clatter.WithStaticKEMKey(identity.kem),
		clatter.WithPrologue(prologue),
		clatter.WithObserver(observer),
	)
	if err != nil {
		return nil, "", err
	}
	payload := make([]byte, clatter.MaxMessageLen)
	payloadLen, err := handshake.ReadMessage(request, payload)
	if err != nil {
		handshake.Destroy()
		return nil, "", err
	}
	if len(observer.remoteStaticDH) == 0 || len(observer.remoteStaticKEM) == 0 {
		handshake.Destroy()
		return nil, "", errors.New("handshake request is missing initiator static key")
	}
	publicKey := RemotePublicKey{
		Suite:             noiseChannelSuite,
		X25519PublicKey:   base64.StdEncoding.EncodeToString(observer.remoteStaticDH),
		MLKEM768PublicKey: base64.StdEncoding.EncodeToString(observer.remoteStaticKEM),
	}
	return &pendingRemoteNoiseHandshake{handshake: handshake, publicKey: publicKey}, string(payload[:payloadLen]), nil
}

func (p *pendingRemoteNoiseHandshake) Complete() (*clatter.TransportState, []byte, error) {
	if p == nil || p.handshake == nil {
		return nil, nil, errors.New("Noise handshake is not available")
	}
	response := make([]byte, clatter.MaxMessageLen)
	responseLen, err := p.handshake.WriteMessage(nil, response)
	if err != nil {
		p.handshake.Destroy()
		p.handshake = nil
		return nil, nil, err
	}
	transport, err := p.handshake.Finalize()
	p.handshake = nil
	if err != nil {
		return nil, nil, err
	}
	return transport, response[:responseLen], nil
}

func (p *pendingRemoteNoiseHandshake) Destroy() {
	if p == nil || p.handshake == nil {
		return
	}
	p.handshake.Destroy()
	p.handshake = nil
}
