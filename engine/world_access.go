package zztgo

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type WorldAccess struct {
	OwnerAccountID         string   `json:"ownerAccountId,omitempty"`
	OwnerName              string   `json:"ownerName,omitempty"`
	CollaboratorAccountIDs []string `json:"collaboratorAccountIds,omitempty"`
}

func (a WorldAccess) CanEdit(accountID string) bool {
	if a.OwnerAccountID == "" {
		return true
	}
	if accountID == "" {
		return false
	}
	if accountID == a.OwnerAccountID {
		return true
	}
	for _, collaborator := range a.CollaboratorAccountIDs {
		if collaborator == accountID {
			return true
		}
	}
	return false
}

func (a WorldAccess) IsOwner(accountID string) bool {
	return a.OwnerAccountID != "" && accountID == a.OwnerAccountID
}

func (a *WorldAccess) AddCollaborator(accountID string) {
	if accountID == "" || accountID == a.OwnerAccountID {
		return
	}
	for _, collaborator := range a.CollaboratorAccountIDs {
		if collaborator == accountID {
			return
		}
	}
	a.CollaboratorAccountIDs = append(a.CollaboratorAccountIDs, accountID)
}

func loadWorldAccess(dir, worldName string) (WorldAccess, bool, error) {
	path := worldAccessPath(dir, worldName)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorldAccess{}, false, nil
		}
		return WorldAccess{}, false, err
	}
	var access WorldAccess
	if err := json.Unmarshal(data, &access); err != nil {
		return WorldAccess{}, false, err
	}
	return access, true, nil
}

func writeWorldAccess(dir, worldName string, access WorldAccess) error {
	data, err := json.MarshalIndent(access, "", "  ")
	if err != nil {
		return err
	}
	tmp := worldAccessPath(dir, worldName) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, worldAccessPath(dir, worldName)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func worldAccessPath(dir, worldName string) string {
	return filepath.Join(dir, worldName+".access.json")
}
