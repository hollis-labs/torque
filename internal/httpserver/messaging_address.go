package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	gomsg "github.com/hollis-labs/go-messaging"
)

// messagingAddressShape names both accepted forms of a messaging address, so
// a caller that sent the wrong one learns the right one from the 400
// (CW-20261001-0014, CW-20261001-0025).
const messagingAddressShape = `msg://<kind>/<authority>/<id> string or {"kind","authority","id"} object`

// messagingAddressObject is the structured form of a messaging address. The
// URN's middle segment is the library's Address.Authority; "scope" is
// accepted as a synonym because it is the name callers reached for first.
type messagingAddressObject struct {
	Kind      string `json:"kind"`
	Authority string `json:"authority"`
	Scope     string `json:"scope"`
	ID        string `json:"id"`
	SubID     string `json:"sub_id"`
}

// parseMessagingAddress decodes one address field of a messaging request
// body, either the canonical URN string or the object form, into the same
// gomsg.Address. Every HTTP surface that takes an address in a body uses it:
// from/to on /broker/send, /broker/request and /messages, and recipient on
// /messages/{id}/consume. The request structs hold the raw value rather than
// a type with its own UnmarshalJSON because only the caller knows which field
// it is parsing, and the 400 has to name it.
//
// An absent or null value is the zero Address, which each handler reports
// as missing (422).
func parseMessagingAddress(field string, raw json.RawMessage) (gomsg.Address, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return gomsg.Address{}, nil
	}
	reject := func(got string) (gomsg.Address, error) {
		return gomsg.Address{}, fmt.Errorf("%q must be a %s; got %s", field, messagingAddressShape, got)
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return reject("a malformed string")
		}
		addr, err := gomsg.ParseURN(s)
		if err != nil {
			return reject(strconv.Quote(s))
		}
		return addr, nil
	case '{':
		var obj messagingAddressObject
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&obj); err != nil {
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) && typeErr.Field != "" {
				return reject(fmt.Sprintf("an object whose %q is a %s", typeErr.Field, typeErr.Value))
			}
			return reject("an object: " + strings.TrimPrefix(err.Error(), "json: "))
		}
		authority := obj.Authority
		if obj.Scope != "" {
			if authority != "" && authority != obj.Scope {
				return reject(`an object whose "authority" and "scope" disagree`)
			}
			authority = obj.Scope
		}
		switch {
		case obj.Kind == "":
			return reject(`an object without "kind"`)
		case authority == "":
			return reject(`an object without "authority"`)
		case obj.ID == "":
			return reject(`an object without "id"`)
		}
		// A "/" inside a value would re-split into a different, valid-looking
		// URN, so it is refused before ParseURN sees the rendered string.
		for _, seg := range []struct{ key, val string }{
			{"kind", obj.Kind}, {"authority", authority}, {"id", obj.ID}, {"sub_id", obj.SubID},
		} {
			if strings.Contains(seg.val, "/") {
				return reject(fmt.Sprintf(`an object whose %q contains "/"`, seg.key))
			}
		}
		addr := gomsg.Address{
			Kind:      gomsg.AddressKind(obj.Kind),
			Authority: authority,
			ID:        obj.ID,
			SubID:     obj.SubID,
		}
		// ParseURN owns the kind vocabulary and segment rules.
		if _, err := gomsg.ParseURN(addr.URN()); err != nil {
			return reject("an object that renders as the invalid address " + strconv.Quote(addr.URN()))
		}
		return addr, nil
	case '[':
		return reject("an array")
	case 't', 'f':
		return reject("a boolean")
	default:
		return reject("a number")
	}
}

// parseMessagingFromTo parses the from/to pair every send shares.
func parseMessagingFromTo(from, to json.RawMessage) (gomsg.Address, gomsg.Address, error) {
	f, err := parseMessagingAddress("from", from)
	if err != nil {
		return gomsg.Address{}, gomsg.Address{}, err
	}
	t, err := parseMessagingAddress("to", to)
	if err != nil {
		return gomsg.Address{}, gomsg.Address{}, err
	}
	return f, t, nil
}
