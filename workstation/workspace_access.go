package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

type workspaceAccess struct {
	Owner string               `json:"owner"`
	Roles map[string]string    `json:"roles"`
	Local *localWorkspaceOwner `json:"local,omitempty"`
}

type localWorkspaceOwner struct {
	Actor      string `json:"actor"`
	NotesOwner string `json:"notesOwner"`
}

var errLocalOwnerSession = errors.New("the existing local owner session is needed to initialize desktop recovery")

func loadWorkspaceAccess(root *os.Root) (workspaceAccess, error) {
	access := workspaceAccess{Roles: map[string]string{}}
	data, err := readNoteFile(root, "access.json", 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return access, nil
	}
	if err != nil {
		return access, err
	}
	if err = json.Unmarshal(data, &access); err != nil {
		return access, err
	}
	if access.Roles == nil {
		return access, fmt.Errorf("saved workspace membership is unreadable")
	}
	for actor, role := range access.Roles {
		if !validNoteID(actor) || role != "owner" && role != "editor" && role != "viewer" || role == "owner" && actor != access.Owner {
			return access, fmt.Errorf("invalid saved workspace membership")
		}
	}
	if access.Owner != "" && access.Roles[access.Owner] != "owner" {
		return access, fmt.Errorf("saved workspace owner is invalid")
	}
	if access.Local != nil && (access.Local.Actor != access.Owner || access.Local.NotesOwner == "") {
		return access, fmt.Errorf("saved local workspace identity is invalid")
	}
	return access, nil
}

// The native startup capability restores the profile's local identity even
// after its browser session expires. Other browser identities remain distinct.
func (s *noteStore) localOwner(actor, notesOwner string) (localWorkspaceOwner, error) {
	var identity localWorkspaceOwner
	err := s.locked(func(root *os.Root) error {
		access, err := loadWorkspaceAccess(root)
		if err != nil {
			return err
		}
		if access.Local != nil {
			identity = *access.Local
			return nil
		}
		if access.Owner != "" {
			if actor != access.Owner || notesOwner == "" {
				return errLocalOwnerSession
			}
			identity = localWorkspaceOwner{Actor: actor, NotesOwner: notesOwner}
		} else {
			identity = localWorkspaceOwner{Actor: collaborationID(), NotesOwner: rand.Text()}
			access.Owner, access.Roles[identity.Actor] = identity.Actor, "owner"
		}
		access.Local = &identity
		return saveWorkspaceAccess(root, access)
	})
	return identity, err
}

func saveWorkspaceAccess(root *os.Root, access workspaceAccess) error {
	data, err := json.Marshal(access)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("workspace membership exceeds its size limit")
	}
	return writeNoteFile(root, "access.json", data)
}

// The encrypted cookie names the actor; only this server-owned table grants
// access. Read and update it under the profile lock, including across windows.
func (s *noteStore) membership(actor string, register, claim bool) (workspaceAccess, error) {
	var access workspaceAccess
	if !register {
		// Membership is published atomically. Existing performers must not
		// wait for a checkpoint's file sync to authorize note-on/off or Stop.
		root, err := os.OpenRoot(s.dir)
		if err != nil {
			return access, err
		}
		access, err = loadWorkspaceAccess(root)
		root.Close()
		if err != nil || !claim || access.Owner != "" || access.Roles[actor] == "" {
			return access, err
		}
	}
	err := s.locked(func(root *os.Root) error {
		var err error
		access, err = loadWorkspaceAccess(root)
		if err != nil {
			return err
		}
		changed := false
		if register && validNoteID(actor) && access.Roles[actor] == "" {
			access.Roles[actor] = "viewer"
			changed = true
		}
		if claim && actor != "" && access.Roles[actor] != "" && access.Owner == "" {
			access.Owner, access.Roles[actor] = actor, "owner"
			changed = true
		}
		if changed {
			return saveWorkspaceAccess(root, access)
		}
		return nil
	})
	return access, err
}

func (s *noteStore) grantMember(actor, role, owner string) (workspaceAccess, error) {
	var access workspaceAccess
	err := s.locked(func(root *os.Root) error {
		var err error
		access, err = loadWorkspaceAccess(root)
		if err != nil {
			return err
		}
		if owner == "" || access.Owner != owner || access.Roles[actor] == "" || access.Roles[actor] == "owner" || role != "viewer" && role != "editor" {
			return fmt.Errorf("workspace membership changed")
		}
		access.Roles[actor] = role
		return saveWorkspaceAccess(root, access)
	})
	return access, err
}
