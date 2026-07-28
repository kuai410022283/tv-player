// Package license 提供 VIP 授权订阅功能。
// 核心逻辑：硬件指纹采集 + AES 加解密 + 授权验证。
package license

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"
)

// ── 授权状态 ───────────────────────────────────────────

// Status 代表当前授权状态
type Status string

const (
	StatusUnlicensed  Status = "unlicensed"  // 未授权
	StatusActivated   Status = "activated"   // 已激活
	StatusExpired     Status = "expired"     // 已过期
	StatusUnsupported Status = "unsupported" // 当前环境不支持（无法采集硬件指纹）
)

// Info 包含当前授权信息
type Info struct {
	Status      Status `json:"status"`
	MachineID   string `json:"machine_id"`
	Features    string `json:"features"`   // 授权功能列表，逗号分隔
	ExpiresAt   string `json:"expires_at"` // "2027-12-31" 或 "permanent"
	ActivatedAt string `json:"activated_at,omitempty"`
}

// ── 全局状态 ───────────────────────────────────────────

var (
	mu         sync.RWMutex
	gMachineID string         // 本机机器码
	gInfo      *Info          // 当前授权信息
	gLicenseDB LicenseStorage // 持久化存储接口
)

// LicenseStorage 授权信息的持久化存储接口
type LicenseStorage interface {
	// Load 加载已激活的授权信息
	Load() (licenseKey, machineID, features, expiresAt, seq, activatedAt string, err error)
	// Save 保存激活的授权信息
	Save(licenseKey, machineID, features, expiresAt, seq string) error
	// Delete 删除授权信息（吊销时）
	Delete() error
	// SeqExists 检查序列号是否已在其他机器上使用过（防跨机器重放）
	SeqExists(seq, machineID string) (bool, error)
}

// ── 初始化 ─────────────────────────────────────────────

// Init 初始化授权模块，采集硬件指纹。
// 如果采集不到硬件指纹，标记为 Unsupported，授权功能不可用。
func Init(storage LicenseStorage) {
	gLicenseDB = storage

	mid := collectMachineID()
	if mid == "" {
		slog.Warn("license: 无法采集硬件指纹，当前环境不支持授权功能")
		mu.Lock()
		gMachineID = ""
		gInfo = &Info{Status: StatusUnsupported, MachineID: ""}
		mu.Unlock()
		return
	}

	mu.Lock()
	gMachineID = mid
	mu.Unlock()

	slog.Info("license: 硬件指纹采集成功", "machine_id", mid[:8]+"...")

	// 尝试从数据库加载已激活授权并验证
	loadAndVerify()
}

// loadAndVerify 从数据库加载授权信息并重新验证
func loadAndVerify() {
	if gLicenseDB == nil {
		return
	}

	licenseKey, _, features, _, seq, activatedAt, err := gLicenseDB.Load()
	if err != nil || licenseKey == "" {
		return // 没激活过，正常
	}

	// 重新验证授权码（去除格式化分隔符，还原 base64url 字符）
	licenseKey = strings.ReplaceAll(licenseKey, " ", "")
	licenseKey = strings.ReplaceAll(licenseKey, "-", "")  // 去掉可视化分隔符
	licenseKey = strings.ReplaceAll(licenseKey, ".", "-") // 还原 base64url 中的 "-"
	decrypted, err := decryptLicenseKey(licenseKey)
	if err != nil {
		slog.Warn("license: 存储的授权码解密失败，需要重新激活", "error", err)
		_ = gLicenseDB.Delete()
		return
	}

	// 解析明文
	parts := strings.SplitN(decrypted, "|", 4)
	if len(parts) != 4 {
		_ = gLicenseDB.Delete()
		return
	}
	decMachineID := parts[0]
	decExpire := parts[1]
	decIdentity := parts[2]
	decSeq := parts[3]

	// 验证身份标识
	if decIdentity != IdentityMarker {
		slog.Warn("license: 存储的授权码身份标识无效")
		_ = gLicenseDB.Delete()
		return
	}

	// 验证机器码
	if decMachineID != gMachineID {
		slog.Warn("license: 存储的授权码与本机机器码不匹配，需要重新激活")
		_ = gLicenseDB.Delete()
		return
	}

	// 验证序列号一致性
	if decSeq != seq {
		_ = gLicenseDB.Delete()
		return
	}

	// 检查是否过期
	if decExpire != "permanent" {
		expDate, err := time.Parse("2006-01-02", decExpire)
		if err != nil || time.Now().After(expDate) {
			slog.Warn("license: 授权已过期", "expires_at", decExpire)
			_ = gLicenseDB.Delete()
			mu.Lock()
			gInfo = &Info{Status: StatusExpired, MachineID: gMachineID, ExpiresAt: decExpire}
			mu.Unlock()
			return
		}
	}

	// 全部通过
	activeExpire := decExpire
	if activeExpire == "permanent" {
		activeExpire = ""
	}
	mu.Lock()
	gInfo = &Info{
		Status:      StatusActivated,
		MachineID:   gMachineID,
		Features:    features,
		ExpiresAt:   activeExpire,
		ActivatedAt: activatedAt,
	}
	mu.Unlock()
	slog.Info("license: 授权验证通过")
}

// ── 公开查询方法 ───────────────────────────────────────

// GetInfo 返回当前授权信息（线程安全）
func GetInfo() Info {
	mu.RLock()
	defer mu.RUnlock()
	if gInfo == nil {
		return Info{Status: StatusUnlicensed, MachineID: gMachineID}
	}
	return *gInfo
}

// GetMachineID 返回本机机器码
func GetMachineID() string {
	mu.RLock()
	defer mu.RUnlock()
	return gMachineID
}

// IsActivated 检查是否已激活授权
func IsActivated() bool {
	mu.RLock()
	defer mu.RUnlock()
	return gInfo != nil && gInfo.Status == StatusActivated
}

// IsSupported 检查当前环境是否支持授权功能
func IsSupported() bool {
	mu.RLock()
	defer mu.RUnlock()
	return gMachineID != ""
}

// ── 激活授权 ───────────────────────────────────────────

// Activate 验证并激活授权码。
// licenseKey: 用户输入的原始授权码（含连字符）。
// 返回激活后的授权信息。
func Activate(licenseKey string) (*Info, error) {
	mu.Lock()
	defer mu.Unlock()

	if gMachineID == "" {
		return nil, fmt.Errorf("当前环境不支持硬件指纹采集，无法激活授权")
	}

	// 去除格式化分隔符：先去空格，再去连字符（分隔符），最后将 "." 还原为 base64url 的 "-"
	// 注意：授权码格式化时 base64url 的 "-" 已被替换为 "."
	cleanKey := strings.ReplaceAll(licenseKey, " ", "")
	cleanKey = strings.ReplaceAll(cleanKey, "-", "")  // 去掉分隔符
	cleanKey = strings.ReplaceAll(cleanKey, ".", "-") // 还原 base64url 中的 "-"

	// 解密
	decrypted, err := decryptLicenseKey(cleanKey)
	if err != nil {
		return nil, fmt.Errorf("授权码无效")
	}

	// 解析明文: 机器码|过期日期|身份标识|UUID
	parts := strings.SplitN(decrypted, "|", 4)
	if len(parts) != 4 {
		return nil, fmt.Errorf("授权码格式错误")
	}

	decMachineID := parts[0]
	decExpire := parts[1]
	decIdentity := parts[2]
	decSeq := parts[3]

	// 验证身份标识
	if decIdentity != IdentityMarker {
		return nil, fmt.Errorf("授权码无效")
	}

	// 验证机器码
	if decMachineID != gMachineID {
		return nil, fmt.Errorf("授权码不适用于本服务器")
	}

	// 检查序列号是否已在其他机器上使用（防跨机器重放）
	// 同机器码允许重复激活，以支持重装软件后重新激活
	if gLicenseDB != nil {
		exists, err := gLicenseDB.SeqExists(decSeq, gMachineID)
		if err != nil {
			slog.Warn("license: 序列号检查失败", "error", err)
		}
		if err == nil && exists {
			return nil, fmt.Errorf("授权码已在其他服务器上使用")
		}
	}

	// 检查是否过期
	expiresDisplay := ""
	if decExpire != "permanent" {
		expDate, err := time.Parse("2006-01-02", decExpire)
		if err != nil {
			return nil, fmt.Errorf("授权码过期日期格式错误")
		}
		if time.Now().After(expDate) {
			return nil, fmt.Errorf("授权码已过期")
		}
		expiresDisplay = decExpire
	}

	// 全部通过 → 写入数据库
	if gLicenseDB != nil {
		if err := gLicenseDB.Save(licenseKey, gMachineID, "", decExpire, decSeq); err != nil {
			return nil, fmt.Errorf("保存授权信息失败: %w", err)
		}
	}

	// 更新内存状态
	gInfo = &Info{
		Status:      StatusActivated,
		MachineID:   gMachineID,
		ExpiresAt:   expiresDisplay,
		ActivatedAt: time.Now().Format(time.RFC3339),
	}

	slog.Info("license: 授权激活成功", "expires_at", decExpire)
	return gInfo, nil
}

// Revoke 吊销当前授权
func Revoke() error {
	mu.Lock()
	defer mu.Unlock()

	if gLicenseDB != nil {
		if err := gLicenseDB.Delete(); err != nil {
			return err
		}
	}

	gInfo = &Info{Status: StatusUnlicensed, MachineID: gMachineID}
	slog.Info("license: 授权已吊销")
	return nil
}

// ── 硬件指纹采集 ───────────────────────────────────────

// collectMachineID 采集本机硬件指纹。
// 多源降级策略：DMI UUID → DMI Serial → 物理 MAC 合集 → 磁盘序列号。
// 全部为硬件级别标识，重装系统不影响，只取第一个可用的来源。
// 全部失败返回空字符串。
func collectMachineID() string {
	var source string

	switch runtime.GOOS {
	case "linux":
		// 来源①：DMI UUID（主板级别，最稳定，部分系统需 root）
		if uuid := readSysfsFile("/sys/class/dmi/id/product_uuid"); uuid != "" {
			source = uuid
			break
		}
		// 来源②：DMI Serial（主板序列号）
		if s := readSysfsFile("/sys/class/dmi/id/product_serial"); s != "" {
			source = s
			break
		}
		// 来源③：物理 MAC 合集（所有物理网卡的永久 MAC，排序后合并）
		// 不受 MAC 随机化、网卡启停、多网卡选择影响
		if macs := readLinuxPhysicalMACs(); len(macs) > 0 {
			sort.Strings(macs)
			source = strings.Join(macs, "|")
			break
		}
		// 来源④：磁盘序列号
		if disk := readDiskSerial(); disk != "" {
			source = disk
		}
	case "windows":
		if uuid := readWindowsSystemUUID(); uuid != "" {
			source = uuid
		}
		if source == "" {
			if macs := readWindowsPhysicalMACs(); len(macs) > 0 {
				sort.Strings(macs)
				source = strings.Join(macs, "|")
			}
		}
	default:
		return ""
	}

	if source == "" {
		return ""
	}

	hash := sha256.Sum256([]byte(source))
	return hex.EncodeToString(hash[:8])
}

// readSysfsFile 读取 Linux sysfs 文件，忽略空值和无效值
func readSysfsFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	val := strings.TrimSpace(string(data))
	if val == "" || val == "Not Settable" || val == "Not Present" ||
		val == "00000000-0000-0000-0000-000000000000" ||
		val == "00:00:00:00:00:00" {
		return ""
	}
	return val
}

// readLinuxPhysicalMACs 读取 Linux 下所有物理网卡的永久 MAC 地址。
// 通过 /sys/class/net/*/addr_assign_type 判断是否为永久 MAC（值为 0 表示永久）。
// 不受 MAC 随机化、网卡 up/down 状态影响。
func readLinuxPhysicalMACs() []string {
	devices, err := os.ReadDir("/sys/class/net")
	if err != nil {
		return nil
	}

	var macs []string
	for _, dev := range devices {
		name := dev.Name()

		// 跳过虚拟网卡
		if strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
			strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "tun") ||
			strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "lxc") ||
			strings.HasPrefix(name, "lo") {
			continue
		}

		// 检查 addr_assign_type：0 = 永久 MAC，1 = 随机 MAC
		assignType := readSysfsFile("/sys/class/net/" + name + "/addr_assign_type")
		if assignType != "0" {
			continue // 跳过随机 MAC
		}

		// 读取 MAC 地址
		mac := readSysfsFile("/sys/class/net/" + name + "/address")
		if mac != "" {
			macs = append(macs, strings.ToUpper(mac))
		}
	}

	return macs
}

// readWindowsPhysicalMACs 读取 Windows 下所有物理网卡的 MAC 地址
func readWindowsPhysicalMACs() []string {
	// 使用 wmic 获取物理网卡 MAC（排除虚拟、蓝牙、VPN 等）
	data, err := execCommand("wmic", "nic", "where", "PhysicalAdapter=True", "get", "MACAddress")
	if err != nil {
		// 降级尝试 PowerShell
		data, err = execCommand("powershell", "-Command",
			"Get-NetAdapter | Where-Object {$_.PhysicalMediaType -ne 0 -and $_.Status -ne 'Disconnected'} | Select-Object -ExpandProperty MacAddress")
		if err != nil {
			return nil
		}
	}

	lines := strings.Split(strings.TrimSpace(data), "\n")
	var macs []string
	for _, line := range lines {
		mac := strings.TrimSpace(line)
		if mac != "" && !strings.EqualFold(mac, "MACAddress") && !strings.EqualFold(mac, "MacAddress") &&
			mac != "00:00:00:00:00:00" {
			macs = append(macs, strings.ToUpper(mac))
		}
	}
	return macs
}

// readDiskSerial 读取第一块磁盘的序列号
func readDiskSerial() string {
	// 尝试多个常见磁盘设备路径
	devices := []string{"sda", "nvme0n1", "vda", "mmcblk0", "xda"}
	for _, dev := range devices {
		serial := readSysfsFile(filepath.Join("/sys/block", dev, "device/serial"))
		if serial != "" {
			return serial
		}
	}
	// 最后尝试通过 /dev/disk/by-id/ 获取
	ids, err := os.ReadDir("/dev/disk/by-id/")
	if err != nil {
		return ""
	}
	for _, id := range ids {
		name := id.Name()
		if strings.HasPrefix(name, "ata-") || strings.HasPrefix(name, "nvme-") ||
			strings.HasPrefix(name, "mmc-") || strings.HasPrefix(name, "scsi-") {
			link, _ := os.Readlink(filepath.Join("/dev/disk/by-id/", name))
			if link != "" && !strings.HasSuffix(link, "-part") {
				return name
			}
		}
	}
	return ""
}

func readWindowsSystemUUID() string {
	// 尝试使用 WMI 查询
	data, err := execCommand("wmic", "csproduct", "get", "uuid")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.EqualFold(line, "UUID") && len(line) >= 32 {
			return line
		}
	}
	return ""
}

// execCommand 执行系统命令（在 Windows 上使用）
var execCommand = func(name string, args ...string) (string, error) {
	return "", fmt.Errorf("not implemented")
}

// SetExecCommand 设置命令执行函数（由 init 在对应平台设置）
func SetExecCommand(fn func(name string, args ...string) (string, error)) {
	execCommand = fn
}

// ── AES 加解密 ─────────────────────────────────────────

// IdentityMarker 身份标识，用于验证授权码来源
const IdentityMarker = "MP"

// decryptLicenseKey 解密授权码，返回明文。
// 输入：base64url 编码的密文（无连字符）
// 输出：机器码|过期日期|身份标识|UUID
func decryptLicenseKey(encoded string) (string, error) {
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}

	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

// EncryptLicense 加密授权码（仅供 license-gen 工具使用）。
// 输入：机器码|过期日期|身份标识|UUID
// 输出：base64url 编码的密文
func EncryptLicense(plaintext string) (string, error) {
	key := deriveKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

// deriveKey 从种子密钥派生 AES-256 密钥
func deriveKey() []byte {
	// 使用 PBKDF2 派生密钥，增加逆向难度
	return pbkdf2.Key([]byte(embeddedSecret), []byte("MediaPlayer-VIP"), 10000, 32, sha256.New)
}
