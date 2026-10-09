// Package jsonany carries free-form JSON through the generated, reflection-free
// codecs of Popcorn Web.
//
// The tinybind generator plans a struct field by its declared type, so a field
// whose JSON shape is open (extracted document metadata, for example) cannot
// be described to it. A type from another package that carries its own codec
// can: Map encodes and decodes itself through jsonbind's type-switch based
// helpers, which walk values without reflection.
package jsonany

import "github.com/shibukawa/tinybind-go/jsonbind"

// Map is a JSON object whose members are not known ahead of time.
type Map map[string]any

// AppendJSONTo implements jsonbind.Appender. A nil map encodes as an empty
// object rather than null, so a client always finds an object there.
func (m Map) AppendJSONTo(dst []byte) []byte {
	if m == nil {
		return append(dst, '{', '}')
	}
	return jsonbind.AppendAny(dst, map[string]any(m))
}

// DecodeJSONFrom implements jsonbind.Decoder. A JSON null leaves the map nil.
func (m *Map) DecodeJSONFrom(data []byte) error {
	p := jsonbind.NewParser(data)
	if p.IsNull() {
		*m = nil
		return nil
	}
	value, err := p.Any()
	if err != nil {
		return err
	}
	if err := p.End(); err != nil {
		return err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return jsonbind.FieldError("", "expected a JSON object", nil)
	}
	*m = Map(object)
	return nil
}
