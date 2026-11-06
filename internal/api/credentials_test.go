package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestDeploymentCredentialAPI(t *testing.T) {
	h := newHarness(t)

	data := h.mustDo(t, "POST", "/setup", map[string]string{
		"token": "setup-token", "email": "admin@example.com", "password": "correct-horse-battery-staple",
	}, "", http.StatusCreated)
	var me struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(data, &me); err != nil {
		t.Fatal(err)
	}
	h.csrf = me.CSRF

	data = h.mustDo(t, "POST", "/deployment-credentials", map[string]any{
		"name": "Tencent prod", "type": "tencent",
		"config": map[string]string{"secret_id": "AKIDsecret", "secret_key": "very-secret"},
	}, h.csrf, http.StatusCreated)
	if strings.Contains(string(data), "very-secret") || strings.Contains(string(data), "AKIDsecret") {
		t.Fatal("API leaked the credential secret")
	}
	var created struct {
		ID               string   `json:"id"`
		Type             string   `json:"type"`
		ConfiguredFields []string `json:"configured_fields"`
	}
	_ = json.Unmarshal(data, &created)
	if created.ID == "" || created.Type != "tencent" || len(created.ConfiguredFields) != 2 {
		t.Fatalf("created = %s", data)
	}

	data = h.mustDo(t, "GET", "/deployment-credentials", nil, "", http.StatusOK)
	var list struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
		Types []string `json:"types"`
	}
	_ = json.Unmarshal(data, &list)
	if len(list.Items) != 1 || len(list.Types) != 6 {
		t.Fatalf("list = %s", data)
	}

	h.mustDo(t, "PATCH", "/deployment-credentials/"+created.ID, map[string]any{
		"name": "Tencent staging", "type": "tencent",
	}, h.csrf, http.StatusOK)

	// Invalid credentials are rejected before storage.
	if status, _ := h.do(t, "POST", "/deployment-credentials", map[string]any{
		"name": "bad", "type": "tencent", "config": map[string]string{"secret_id": "only"},
	}, h.csrf); status != http.StatusBadRequest && status != http.StatusUnprocessableEntity {
		t.Fatalf("invalid credential status = %d", status)
	}

	h.mustDo(t, "DELETE", "/deployment-credentials/"+created.ID, nil, h.csrf, http.StatusNoContent)
	data = h.mustDo(t, "GET", "/deployment-credentials", nil, "", http.StatusOK)
	_ = json.Unmarshal(data, &list)
	if len(list.Items) != 0 {
		t.Fatalf("credential was not deleted: %s", data)
	}
}
