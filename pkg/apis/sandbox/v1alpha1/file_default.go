package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func validAction(a Action) bool { return a == ActionAllow || a == ActionDeny }

// Validate rejects ambiguous or incomplete representations. Kubernetes cannot
// strongly type a string-or-object structural schema, so consumers must call
// this validation before compiling the file section.
func (d FileDefault) Validate() error {
	if d.PerAccess == nil {
		if validAction(d.Action) {
			return nil
		}
		return fmt.Errorf("file default must be allow, deny, or a complete per-access object")
	}
	if d.Action != "" {
		return fmt.Errorf("file default cannot contain both scalar and per-access defaults")
	}
	p := d.PerAccess
	for _, a := range []Action{p.Read, p.Write, p.Create, p.Delete, p.Rename, p.Chmod, p.ExecMap} {
		if !validAction(a) {
			return fmt.Errorf("every per-access file default must be allow or deny")
		}
	}
	return nil
}
func (d FileDefault) MarshalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	if d.PerAccess != nil {
		return json.Marshal(d.PerAccess)
	}
	return json.Marshal(d.Action)
}
func (d *FileDefault) UnmarshalJSON(data []byte) error {
	var next FileDefault
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return fmt.Errorf("empty file default")
	}
	if data[0] == '"' {
		if err := json.Unmarshal(data, &next.Action); err != nil {
			return err
		}
	} else if data[0] == '{' {
		next.PerAccess = &FileAccessDefaults{}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(next.PerAccess); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("unexpected data after file default")
		}
	} else {
		return fmt.Errorf("file default must be a string or object")
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*d = next
	return nil
}
