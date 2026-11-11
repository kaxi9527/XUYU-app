package modelhttp

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/kaxi9527/WorkShelf/internal/models"
)

// Optional monetary evidence must be unambiguous. Ignore invalid/missing
// receipts without discarding valid model text, and never infer a zero debit.
func openRouterDebit(target models.Target, data []byte) *models.OpenRouterDebit {
	if !models.SupportsOpenRouterDebit(target.Protocol, target.BaseURL) {
		return nil
	}
	root := uniqueObject(data)
	u := uniqueObject(root["usage"])
	amount, ok := models.NormalizeDebitAmount(string(u["cost"]))
	if !ok {
		return nil
	}
	d := &models.OpenRouterDebit{Amount: amount}
	if raw, exists := u["is_byok"]; exists && !bytes.Equal(raw, []byte("null")) {
		var byok bool
		if json.Unmarshal(raw, &byok) != nil {
			return nil
		}
		d.BYOK = &byok
	}
	return d
}

func uniqueObject(data []byte) map[string]json.RawMessage {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok {
			return nil
		}
		if _, exists := fields[key]; exists {
			return nil
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil
		}
		fields[key] = bytes.TrimSpace(value)
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return nil
	}
	if _, err = d.Token(); err != io.EOF {
		return nil
	}
	return fields
}
