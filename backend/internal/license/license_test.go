package license

import (
	"sync"
	"testing"
	"time"
)

type MockStorage struct {
	mu             sync.Mutex
	licenseKey     string
	machineID      string
	features       string
	expiresAt      string
	seq            string
	activatedAt    string
	lastVerifiedAt string
	deleted        bool
}

func (m *MockStorage) Load() (string, string, string, string, string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleted {
		return "", "", "", "", "", "", nil
	}
	return m.licenseKey, m.machineID, m.features, m.expiresAt, m.seq, m.activatedAt, nil
}

func (m *MockStorage) Save(licenseKey, machineID, features, expiresAt, seq string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.licenseKey = licenseKey
	m.machineID = machineID
	m.features = features
	m.expiresAt = expiresAt
	m.seq = seq
	m.deleted = false
	return nil
}

func (m *MockStorage) Delete() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleted = true
	return nil
}

func (m *MockStorage) SeqExists(seq, machineID string) (bool, error) {
	return false, nil
}

func (m *MockStorage) UpdateLastVerifiedAt(encryptedTime string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastVerifiedAt = encryptedTime
	return nil
}

func (m *MockStorage) GetLastVerifiedAt() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastVerifiedAt, nil
}

func TestVerifyExpiry_Valid(t *testing.T) {
	mu.Lock()
	gMachineID = "test-machine"
	gInfo = &Info{
		Status:    StatusActivated,
		MachineID: "test-machine",
		ExpiresAt: time.Now().Add(24 * time.Hour).Format("2006-01-02"),
	}
	mockDB := &MockStorage{}
	gLicenseDB = mockDB
	mu.Unlock()

	// Initial verification
	valid := VerifyExpiry()
	if !valid {
		t.Error("expected license to be valid")
	}

	mu.RLock()
	status := gInfo.Status
	mu.RUnlock()
	if status != StatusActivated {
		t.Errorf("expected status to be activated, got %v", status)
	}

	if mockDB.lastVerifiedAt == "" {
		t.Error("expected lastVerifiedAt to be updated in database")
	}
}

func TestVerifyExpiry_Expired(t *testing.T) {
	mu.Lock()
	gMachineID = "test-machine"
	gInfo = &Info{
		Status:    StatusActivated,
		MachineID: "test-machine",
		ExpiresAt: time.Now().Add(-24 * time.Hour).Format("2006-01-02"), // yesterday
	}
	mockDB := &MockStorage{}
	gLicenseDB = mockDB
	mu.Unlock()

	valid := VerifyExpiry()
	if valid {
		t.Error("expected license to be invalid (expired)")
	}

	mu.RLock()
	status := gInfo.Status
	mu.RUnlock()
	if status != StatusExpired {
		t.Errorf("expected status to be expired, got %v", status)
	}

	if mockDB.deleted {
		t.Error("expected database record not to be deleted/marked revoked on soft expiration")
	}
}

func TestVerifyExpiry_RollbackDetection(t *testing.T) {
	mu.Lock()
	gMachineID = "test-machine"
	gInfo = &Info{
		Status:    StatusActivated,
		MachineID: "test-machine",
		ExpiresAt: time.Now().Add(48 * time.Hour).Format("2006-01-02"),
	}
	mockDB := &MockStorage{}
	gLicenseDB = mockDB
	mu.Unlock()

	// Set a future last verified time to simulate clock rollback
	futureTime := time.Now().Add(10 * time.Hour)
	encFuture, err := EncryptLicense(futureTime.Format(time.RFC3339))
	if err != nil {
		t.Fatalf("failed to encrypt future time: %v", err)
	}
	mockDB.lastVerifiedAt = encFuture

	// VerifyExpiry should detect the rollback because current time is before the futureTime
	valid := VerifyExpiry()
	if valid {
		t.Error("expected rollback to be detected and invalid")
	}

	mu.RLock()
	status := gInfo.Status
	mu.RUnlock()
	if status != StatusExpired {
		t.Errorf("expected status to be expired, got %v", status)
	}
}

func TestVerifyExpiry_TamperingDetection(t *testing.T) {
	mu.Lock()
	gMachineID = "test-machine"
	gInfo = &Info{
		Status:    StatusActivated,
		MachineID: "test-machine",
		ExpiresAt: time.Now().Add(48 * time.Hour).Format("2006-01-02"),
	}
	mockDB := &MockStorage{}
	gLicenseDB = mockDB
	mu.Unlock()

	// Set invalid encrypted text to simulate DB tampering
	mockDB.lastVerifiedAt = "invalid-base64-or-bad-ciphertext"

	valid := VerifyExpiry()
	if valid {
		t.Error("expected tampering to be detected and license invalidated")
	}

	mu.RLock()
	status := gInfo.Status
	mu.RUnlock()
	if status != StatusExpired {
		t.Errorf("expected status to be expired, got %v", status)
	}
}
