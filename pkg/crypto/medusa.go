package crypto

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type CryptoAppProfile struct {
	Name                 string
	AID, License         int64
	Package, VersionName string
	Medusa               MedusaParams
}
type MedusaParams struct {
	VersionHex            string
	License               int64
	VersionName, DeviceID string
}

func HongguoCryptoProfile() CryptoAppProfile {
	return CryptoAppProfile{Name: "hongguo", AID: 8662, License: 1588093228, Package: "com.phoenix.read", VersionName: "7.3.9.32", Medusa: MedusaParams{VersionHex: "03000000f7e85ffad7d7dc3bd62ac87057cf6118", License: 1588093228, VersionName: "7.1.3.32"}}
}

type MedusaEntropy struct {
	PayloadNonce, KeyNonce, EnvelopeNonce uint32
	Counter, Delay                        int
}

type protoBytes []byte

func (p *protoBytes) sint(n int, v int64) {
	*p = binary.AppendUvarint(*p, uint64(n<<3))
	*p = binary.AppendUvarint(*p, uint64(v<<1)^uint64(v>>63))
}
func (p *protoBytes) bytes(n int, v []byte) {
	if len(v) == 0 {
		return
	}
	*p = binary.AppendUvarint(*p, uint64(n<<3|2))
	*p = binary.AppendUvarint(*p, uint64(len(v)))
	*p = append(*p, v...)
}
func (p *protoBytes) text(n int, v string) { p.bytes(n, []byte(v)) }
func (p *protoBytes) float(n int, v float64) {
	*p = binary.AppendUvarint(*p, uint64(n<<3|5))
	*p = binary.LittleEndian.AppendUint32(*p, math.Float32bits(float32(v)))
}
func unhex(s string) []byte { v, _ := hex.DecodeString(s); return v }

func deviceProto(profile CryptoAppProfile, device string) []byte {
	var p protoBytes
	p.sint(1, 1)
	p.sint(2, 2)
	p.text(3, strconv.FormatInt(profile.AID, 10))
	p.text(4, device)
	p.text(5, "Ai6svO3PyrwDOUSmO6ZcResxu")
	p.text(6, "!noperm!")
	p.sint(7, -888888)
	p.sint(8, -888888)
	p.sint(9, 3)
	p.sint(10, -888888)
	p.text(11, "!notset!")
	p.text(12, "Asia/Shanghai,8")
	p.text(13, "zh_CN")
	p.sint(14, 4)
	for i, v := range []float64{255.24993896484375, 35.58599090576172, 3.467449188232422, 3.467449188232422, 255.1754913330078, 42.17544174194336} {
		p.float(16+i, v)
	}
	p.text(22, "16")
	p.sint(23, 41)
	p.sint(24, 36)
	for i, v := range []int64{1728388016635, 1728388016635, 1728388016635, 1728388016637, -1} {
		p.sint(25+i, v)
	}
	for i, v := range []string{"25053RT47C", "Redmi", "25053RT47C", "25053RT47C", "Xiaomi", "Redmi", "Redmi"} {
		p.text(30+i, v)
	}
	p.sint(38, 31)
	return p
}

// medusaProtos returns the five serialized layers for byte-level diagnostics.
func medusaProtos(rawURL string, body []byte, timestamp uint32, device string, profile CryptoAppProfile, entropy MedusaEntropy) ([][]byte, error) {
	if entropy.Counter < 0 || entropy.Counter >= 21 || entropy.Delay < 0 || entropy.Delay >= 2000 {
		return nil, fmt.Errorf("medusa: 随机参数越界")
	}
	params := profile.Medusa
	if params.License == 0 {
		params.License = profile.License
	}
	if params.VersionName == "" {
		params.VersionName = profile.VersionName
	}
	if params.DeviceID == "" {
		params.DeviceID = device
	}
	query := ""
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		query = rawURL[i+1:]
	}
	qhash := SM3([]byte(query))
	var bhash [16]byte
	if len(body) > 0 {
		bhash = md5.Sum(body)
	}
	var ts [4]byte
	binary.LittleEndian.PutUint32(ts[:], timestamp)
	f13, err := HashF13(qhash, bhash[:], ts[:], timestamp)
	if err != nil {
		return nil, err
	}
	var p0 protoBytes
	p0.sint(1, 111)
	p0.sint(2, 10)
	p0.sint(3, 694367)
	p0.sint(5, 586952199)
	p1 := deviceProto(profile, params.DeviceID)
	var p2 protoBytes
	p2.sint(1, int64(timestamp))
	p2.sint(2, -2)
	p2.sint(4, 200)
	var p3 protoBytes
	p3.sint(1, int64(entropy.Counter+100))
	p3.sint(2, 146331399)
	p3.sint(3, 146331396)
	p3.sint(5, 7)
	p3.text(6, "v04.06.04.03-bugfix")
	p3.sint(7, int64(entropy.Delay+10001))
	p3.bytes(12, p1)
	p3.bytes(13, p2)
	p3.text(14, params.VersionName)
	material := append(append([]byte(query), bhash[:]...), []byte("none")...)
	var p4 protoBytes
	p4.bytes(1, unhex("f7e85ffad7d7dc3bd62ac87057cf6118"))
	p4.sint(2, 3)
	p4.sint(3, int64(entropy.PayloadNonce))
	p4.text(4, strconv.FormatInt(profile.AID, 10))
	p4.text(5, params.DeviceID)
	p4.text(6, strconv.FormatInt(params.License, 10))
	p4.text(7, params.VersionName)
	p4.text(8, "v04.06.04-ml-android")
	p4.sint(9, 67503104)
	p4.bytes(10, unhex("4001000000000000"))
	p4.sint(12, int64(timestamp))
	p4.bytes(13, f13)
	p4.bytes(14, qhash[:6])
	p4.bytes(15, p0)
	p4.text(16, "AXYQOS6n2m60x1fVZHIrH3iol")
	p4.sint(17, int64(timestamp))
	p4.bytes(19, SM3(material))
	p4.text(20, "none")
	p4.sint(21, 312)
	p4.bytes(23, p3)
	p4.text(24, `{"cmr":16777216,"cmr2":16777216,"un_h":1879194040,"vpn":0,"kd":0,"fkd":3672518972,"pd":-1872573247,"dyn":"","do":0,"tk":true}`)
	return [][]byte{p0, p1, p2, p3, p4}, nil
}

// BuildMedusaWithEntropy reproduces BuildMedusaFor, including its custom AES
// bit carrier. Body must be exactly the wire bytes, after any HTTP gzip pass.
func BuildMedusaWithEntropy(rawURL string, body []byte, timestamp uint32, device string, profile CryptoAppProfile, entropy MedusaEntropy) (string, error) {
	stages, err := medusaProtos(rawURL, body, timestamp, device, profile, entropy)
	if err != nil {
		return "", err
	}
	key, suffix := KeyHash(unhex("8ebdfa3806ecc5cee79423e6029ed82540bc2218bb7eaef71cb691f7aa8aa2f5"), entropy.KeyNonce)
	transformed, err := XMXor(stages[4], key)
	if err != nil {
		return "", err
	}
	data := append(unhex("4001000000000000"), transformed...)
	for l, r := 0, len(data)-1; l < r; l, r = l+1, r-1 {
		data[l], data[r] = data[r], data[l]
	}
	for i := range data {
		data[i] ^= suffix[(^i)&3]
	}
	query := ""
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		query = rawURL[i+1:]
	}
	qhash := SM3([]byte(query))
	var bhash [16]byte
	if len(body) > 0 {
		bhash = md5.Sum(body)
	}
	f13, err := HashF13(qhash, bhash[:], binary.LittleEndian.AppendUint32(nil, timestamp), timestamp)
	if err != nil {
		return "", err
	}
	flags := uint32(qhash[0]&63)<<14 | uint32(f13[0]&63)<<8 | 0x18000001
	envelope := []byte{0x35}
	envelope = binary.LittleEndian.AppendUint32(envelope, entropy.EnvelopeNonce)
	envelope = binary.LittleEndian.AppendUint32(envelope, flags)
	envelope = append(envelope, data...)
	envelope = binary.LittleEndian.AppendUint16(envelope, uint16(entropy.KeyNonce>>16))
	aes, err := newAESV3(unhex("f1593376766ea98d34f31b057a9d5be4"), timestamp)
	if err != nil {
		return "", err
	}
	ciphertext, err := aes.encrypt(envelope, unhex("1fe109a4125283f418de9e051a969e12"))
	if err != nil {
		return "", err
	}
	version := profile.Medusa.VersionHex
	if version == "" {
		version = "050000002d4b4fca49750d433fb5ae2c226dcc56"
	}
	prefix, err := hex.DecodeString(version)
	if err != nil || len(prefix) != 20 {
		return "", fmt.Errorf("medusa: 版本前缀必须是 20 字节 hex")
	}
	for i := 0; i < 20; i += 4 {
		binary.LittleEndian.PutUint32(prefix[i:], binary.LittleEndian.Uint32(prefix[i:])^timestamp)
	}
	prefix = binary.LittleEndian.AppendUint16(prefix, uint16(entropy.KeyNonce))
	prefix = append(prefix, 0, 1)
	prefix = append(prefix, ciphertext...)
	return base64.StdEncoding.EncodeToString(prefix), nil
}
