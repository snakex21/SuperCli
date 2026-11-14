//go:build windows

package mail

import (
	"errors"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The pairing secret is wrapped with DPAPI for the current Windows user, so a
// copy of the data folder is useless on another account or machine.
var thunderbirdDPAPIEntropy = []byte("supercli thunderbird bridge pairing v2")

func thunderbirdProtectSecret(secret []byte) (string, error) {
	in := windows.DataBlob{Size: uint32(len(secret)), Data: &secret[0]}
	entropy := windows.DataBlob{Size: uint32(len(thunderbirdDPAPIEntropy)), Data: &thunderbirdDPAPIEntropy[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	blob := unsafe.Slice(out.Data, out.Size)
	return "dpapi:" + b64.EncodeToString(blob), nil
}

func thunderbirdUnprotectSecret(value string) ([]byte, error) {
	encoded, ok := strings.CutPrefix(value, "dpapi:")
	if !ok {
		return nil, errors.New("pairing secret is not DPAPI-protected")
	}
	blob, err := b64.DecodeString(encoded)
	if err != nil || len(blob) == 0 {
		return nil, errors.New("pairing secret is malformed")
	}
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	entropy := windows.DataBlob{Size: uint32(len(thunderbirdDPAPIEntropy)), Data: &thunderbirdDPAPIEntropy[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

// thunderbirdRestrictPath replaces inherited permissions with a protected DACL
// granting only the current Windows account access.
func thunderbirdRestrictPath(path string) error {
	inheritance := uint32(windows.NO_INHERITANCE)
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       inheritance,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil)
}
