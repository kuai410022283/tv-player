package services

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// ApkModifier 定义了修改反编译后 APK 目录的通用接口
type ApkModifier interface {
	Name() string
	Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error
}

// AppNameModifier 修改应用名称
type AppNameModifier struct{}

func (m *AppNameModifier) Name() string { return "AppNameModifier" }

func (m *AppNameModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	if settings.AppName == "" {
		return nil
	}
	logFunc("  - 应用名称: %s", settings.AppName)
	manifestPath := filepath.Join(tempDir, "AndroidManifest.xml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("读取 AndroidManifest.xml 失败: %w", err)
	}

	manifestStr := string(manifestBytes)

	// 查找 android:label 的值
	labelReg := regexp.MustCompile(`android:label="([^"]*)"`)
	matches := labelReg.FindStringSubmatch(manifestStr)
	if len(matches) < 2 {
		return fmt.Errorf("未在 AndroidManifest.xml 中找到 android:label 属性")
	}
	labelValue := matches[1]

	// 对特殊字符进行 HTML 转义
	escapedName := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	).Replace(settings.AppName)

	if strings.HasPrefix(labelValue, "@string/") {
		stringKey := strings.TrimPrefix(labelValue, "@string/")
		modifiedFiles := 0

		// 遍历 res/values* 目录下的 strings.xml 文件
		resDir := filepath.Join(tempDir, "res")
		err := filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && info.Name() == "strings.xml" {
				dirName := filepath.Base(filepath.Dir(path))
				if strings.HasPrefix(dirName, "values") {
					contentBytes, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					content := string(contentBytes)
					// 用正则匹配并替换指定的 string key（兼容可能带有属性如 translatable="false" 的情况）
					keyReg := regexp.MustCompile(fmt.Sprintf(`<string\s+name="%s"\s*(?:[^>]*)>([\s\S]*?)</string>`, regexp.QuoteMeta(stringKey)))
					if keyReg.MatchString(content) {
						newContent := keyReg.ReplaceAllStringFunc(content, func(match string) string {
							openTagEnd := strings.Index(match, ">")
							if openTagEnd == -1 {
								return match
							}
							return match[:openTagEnd+1] + escapedName + "</string>"
						})
						if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
							return err
						}
						modifiedFiles++
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("遍历 res 目录更新应用名称资源失败: %w", err)
		}
		if modifiedFiles == 0 {
			// 如果 strings.xml 里没找到，降级直接在 AndroidManifest.xml 中硬编码替换
			newManifest := strings.Replace(manifestStr, `android:label="`+labelValue+`"`, `android:label="`+escapedName+`"`, 1)
			if err := os.WriteFile(manifestPath, []byte(newManifest), 0644); err != nil {
				return fmt.Errorf("写入 AndroidManifest.xml 失败: %w", err)
			}
		}
	} else {
		newManifest := strings.Replace(manifestStr, `android:label="`+labelValue+`"`, `android:label="`+escapedName+`"`, 1)
		if err := os.WriteFile(manifestPath, []byte(newManifest), 0644); err != nil {
			return fmt.Errorf("写入 AndroidManifest.xml 失败: %w", err)
		}
	}
	return nil
}

// VersionModifier 修改版本信息
type VersionModifier struct{}

func (m *VersionModifier) Name() string { return "VersionModifier" }

func (m *VersionModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	logFunc("  - 版本信息: %s (%d)", settings.VersionName, settings.VersionCode)
	apktoolYmlPath := filepath.Join(tempDir, "apktool.yml")
	if _, err := os.Stat(apktoolYmlPath); os.IsNotExist(err) {
		return nil
	}

	ymlBytes, err := os.ReadFile(apktoolYmlPath)
	if err != nil {
		return fmt.Errorf("读取 apktool.yml 失败: %w", err)
	}

	ymlStr := string(ymlBytes)

	// 用正则替换 versionName 和 versionCode
	verNameReg := regexp.MustCompile(`versionName: .*`)
	verCodeReg := regexp.MustCompile(`versionCode: .*`)

	ymlStr = verNameReg.ReplaceAllString(ymlStr, fmt.Sprintf("versionName: '%s'", settings.VersionName))
	ymlStr = verCodeReg.ReplaceAllString(ymlStr, fmt.Sprintf("versionCode: %d", settings.VersionCode))

	// 针对 targetSdkVersion >= 30 (Android 11) 的强制要求，确保 resources.arsc 存储时不进行压缩
	if !strings.Contains(ymlStr, "- resources.arsc") {
		if strings.Contains(ymlStr, "doNotCompress:") {
			ymlStr = strings.Replace(ymlStr, "doNotCompress:", "doNotCompress:\n- resources.arsc", 1)
		} else {
			ymlStr += "\ndoNotCompress:\n- resources.arsc\n"
		}
	}

	if err := os.WriteFile(apktoolYmlPath, []byte(ymlStr), 0644); err != nil {
		return fmt.Errorf("写入 apktool.yml 失败: %w", err)
	}
	return nil
}

// LogoAndBannerModifier 替换/合成应用图标及 TV 横版横幅
type LogoAndBannerModifier struct {
	CustomLogoPath   string
	CustomBannerPath string
}

func (m *LogoAndBannerModifier) Name() string { return "LogoAndBannerModifier" }

func (m *LogoAndBannerModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	if m.CustomLogoPath == "" && m.CustomBannerPath == "" {
		return nil
	}

	logFunc("  - 图标与横幅: 已应用定制图片资源")
	resDir := filepath.Join(tempDir, "res")

	// 1. 替换常规应用图标
	if m.CustomLogoPath != "" && fileExists(m.CustomLogoPath) {
		logoBytes, err := os.ReadFile(m.CustomLogoPath)
		if err != nil {
			return fmt.Errorf("读取定制 Logo 失败: %w", err)
		}

		replacedIcons := 0
		err = filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				name := strings.ToLower(info.Name())
				if strings.HasSuffix(name, ".png") && (strings.HasPrefix(name, "ic_launcher") || name == "ic_tv.png" || name == "ic_splash_logo.png" || name == "logo.png") {
					if err := os.WriteFile(path, logoBytes, 0644); err != nil {
						return err
					}
					replacedIcons++
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("彻底替换图标失败: %w", err)
		}

		_ = filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			lowerPath := strings.ToLower(filepath.ToSlash(path))
			name := strings.ToLower(info.Name())
			if strings.Contains(lowerPath, "mipmap-anydpi-v26") && strings.HasSuffix(name, ".xml") && strings.HasPrefix(name, "ic_launcher") {
				if err := os.Remove(path); err == nil {
					replacedIcons++
				}
			}
			return nil
		})
	}

	// 2. 替换/合成 Android TV 桌面宽屏横幅 (banner.png)
	if m.CustomBannerPath != "" && fileExists(m.CustomBannerPath) {
		bannerBytes, err := os.ReadFile(m.CustomBannerPath)
		if err != nil {
			return fmt.Errorf("读取 TV 专属横幅失败: %w", err)
		}

		replacedBanners := 0
		_ = filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.ToLower(info.Name()) == "banner.png" {
				_ = os.WriteFile(path, bannerBytes, 0644)
				replacedBanners++
			}
			return nil
		})

		if replacedBanners == 0 {
			targetBannerPath := filepath.Join(resDir, "drawable", "banner.png")
			_ = os.MkdirAll(filepath.Dir(targetBannerPath), 0755)
			_ = os.WriteFile(targetBannerPath, bannerBytes, 0644)
		}
	} else if m.CustomLogoPath != "" && fileExists(m.CustomLogoPath) {
		logoFile, err := os.Open(m.CustomLogoPath)
		if err == nil {
			defer func() { _ = logoFile.Close() }()
			logoImg, _, err := image.Decode(logoFile)
			if err == nil {
				bannerWidth := 320
				bannerHeight := 180
				bannerImg := image.NewRGBA(image.Rect(0, 0, bannerWidth, bannerHeight))

				bgColor := color.RGBA{R: 1, G: 37, B: 118, A: 255}
				draw.Draw(bannerImg, bannerImg.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

				targetHeight := 120
				origWidth := logoImg.Bounds().Dx()
				origHeight := logoImg.Bounds().Dy()

				targetWidth := int(float64(origWidth) * (float64(targetHeight) / float64(origHeight)))
				if targetWidth > 260 {
					targetWidth = 260
					targetHeight = int(float64(origHeight) * (float64(targetWidth) / float64(origWidth)))
				}

				scaledLogo := resizeImage(logoImg, targetWidth, targetHeight)

				offsetX := (bannerWidth - targetWidth) / 2
				offsetY := (bannerHeight - targetHeight) / 2
				draw.Draw(bannerImg, image.Rect(offsetX, offsetY, offsetX+targetWidth, offsetY+targetHeight), scaledLogo, image.Point{}, draw.Over)

				bannerPath := filepath.Join(resDir, "drawable", "banner.png")
				_ = os.MkdirAll(filepath.Dir(bannerPath), 0755)
				outBannerFile, err := os.OpenFile(bannerPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
				if err == nil {
					_ = png.Encode(outBannerFile, bannerImg)
					_ = outBannerFile.Close()
				}
			}
		}
	}

	return nil
}

// Simple image resize using nearest-neighbor algorithm
func resizeImage(img image.Image, w, h int) image.Image {
	srcBounds := img.Bounds()
	dstBounds := image.Rect(0, 0, w, h)
	dst := image.NewRGBA(dstBounds)

	dx := srcBounds.Dx()
	dy := srcBounds.Dy()

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			srcX := srcBounds.Min.X + (x * dx / w)
			srcY := srcBounds.Min.Y + (y * dy / h)
			dst.Set(x, y, img.At(srcX, srcY))
		}
	}
	return dst
}

// SmaliConfigModifier 修改 Smali 配置文件以实现直连与隐藏二维码
type SmaliConfigModifier struct{}

func (m *SmaliConfigModifier) Name() string { return "SmaliConfigModifier" }

func (m *SmaliConfigModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	// 1. 从 AndroidManifest.xml 读取当前包名，用于定位 Prefs.smali
	manifestPath := filepath.Join(tempDir, "AndroidManifest.xml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("读取 AndroidManifest.xml 失败: %w", err)
	}
	pkgReg := regexp.MustCompile(`package="([^"]+)"`)
	pkgMatches := pkgReg.FindStringSubmatch(string(manifestBytes))
	if len(pkgMatches) < 2 {
		return fmt.Errorf("未在 AndroidManifest.xml 中找到 package 属性")
	}
	currentPkgSlash := strings.ReplaceAll(pkgMatches[1], ".", "/")

	// 查找并遍历所有的 smali 文件以定位 Prefs.smali
	var prefsSmaliPath string
	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), "Prefs.smali") {
			normalizedPath := filepath.ToSlash(path)
			if strings.Contains(normalizedPath, currentPkgSlash) {
				prefsSmaliPath = path
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("查找 Prefs.smali 失败: %w", err)
	}

	if prefsSmaliPath == "" {
		return fmt.Errorf("未在反编译目录中找到 Prefs.smali 配置文件")
	}

	// 2. 替换服务器默认地址为明文
	if settings.DefaultServerURL != "" {
		logFunc("  - 服务器地址: %s", settings.DefaultServerURL)
		err = replaceSmaliConstant(prefsSmaliPath, "DEFAULT_SERVER_URL", settings.DefaultServerURL)
		if err != nil {
			return fmt.Errorf("修改默认服务器地址失败: %w", err)
		}
	}

	// 3. 当设置了服务器地址时，隐藏二维码页面；否则保留扫码页让用户自行配置
	if settings.DefaultServerURL != "" {
		err = replaceSmaliConstant(prefsSmaliPath, "ALLOW_SERVER_CONFIG", false)
		if err != nil {
			return fmt.Errorf("修改 ALLOW_SERVER_CONFIG 常量失败: %w", err)
		}
	}

	// 4. 替换 smali 代码中所有 http://0.0.0.0:9527 占位符为明文地址
	if settings.DefaultServerURL != "" {
		replacedCount := 0
		err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".smali") {
				contentBytes, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if bytes.Contains(contentBytes, []byte("http://0.0.0.0:9527")) {
					newContent := bytes.ReplaceAll(contentBytes, []byte("http://0.0.0.0:9527"), []byte(settings.DefaultServerURL))
					if err := os.WriteFile(path, newContent, 0644); err != nil {
						return err
					}
					replacedCount++
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("遍历并替换硬编码地址失败: %w", err)
		}
	}

	return nil
}

// replaceSmaliConstant 定位并修改 Smali 文件中的静态常量字段
func replaceSmaliConstant(filePath, fieldName string, newValue interface{}) error {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(contentBytes)
	lines := strings.Split(content, "\n")
	modified := false

	for i, line := range lines {
		if strings.Contains(line, fieldName) && strings.Contains(line, ".field") && strings.Contains(line, "static") {
			switch val := newValue.(type) {
			case bool:
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:Z\s*=\s*)(true|false|0x[0-9a-fA-F]+|[0-9]+)`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					replacement := "false"
					if val {
						replacement = "true"
					}
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf("${1}%s", replacement))
					modified = true
				}
			case string:
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:Ljava/lang/String;\s*=\s*)".*"`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf(`${1}"%s"`, val))
					modified = true
				}
			case int:
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:I\s*=\s*)(0x[0-9a-fA-F]+|[0-9\-]+)`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf(`${1}0x%x`, val))
					modified = true
				}
			}
		}
	}

	if !modified {
		return fmt.Errorf("在 %s 中未找到匹配的字段 %s 常量定义进行修改", filepath.Base(filePath), fieldName)
	}

	newContent := strings.Join(lines, "\n")
	return os.WriteFile(filePath, []byte(newContent), 0644)
}

// ExecuteCommandWithLog 封装执行带有 Context 超时控制和标准流捕获的子命令
func ExecuteCommandWithLog(ctx context.Context, cmdName string, args []string, logFunc func(string, ...interface{})) error {
	cmd := exec.CommandContext(ctx, cmdName, args...)

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err != nil {
		errMsg := stderrBuf.String()
		if errMsg == "" {
			errMsg = stdoutBuf.String()
		}
		cleanCmd := filepath.Base(cmdName)
		logFunc("[ERROR] %s 执行失败: %s", cleanCmd, strings.TrimSpace(errMsg))
		return fmt.Errorf("命令执行失败 %s: %w", cleanCmd, err)
	}
	return nil
}

// PackageNameModifier 修改 APK 包名（应用 ID），用于解决特定电视系统的安装限制
type PackageNameModifier struct{}

func (m *PackageNameModifier) Name() string { return "PackageNameModifier" }

func (m *PackageNameModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	if settings.PackageName == "" {
		return nil
	}

	// 1. 从 AndroidManifest.xml 读取原始包名
	manifestPath := filepath.Join(tempDir, "AndroidManifest.xml")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("读取 AndroidManifest.xml 失败: %w", err)
	}
	manifestStr := string(manifestBytes)

	// 查找 package 属性
	pkgReg := regexp.MustCompile(`package="([^"]+)"`)
	matches := pkgReg.FindStringSubmatch(manifestStr)
	if len(matches) < 2 {
		return fmt.Errorf("未在 AndroidManifest.xml 中找到 package 属性")
	}
	oldPkg := matches[1]
	logFunc("  - 应用包名: %s -> %s", oldPkg, settings.PackageName)

	// 2. 更新 AndroidManifest.xml 中的 package 属性及所有组件与 Provider 包名引用
	newManifest := strings.ReplaceAll(manifestStr, oldPkg, settings.PackageName)
	if err := os.WriteFile(manifestPath, []byte(newManifest), 0644); err != nil {
		return fmt.Errorf("写入 AndroidManifest.xml 失败: %w", err)
	}

	// 2.5 更新 res 目录下所有 xml 文件中的包名引用（如 layout 中的自定义 View 或 provider 授权）
	resDir := filepath.Join(tempDir, "res")
	_ = filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(info.Name(), ".xml") {
			contentBytes, err := os.ReadFile(path)
			if err == nil && bytes.Contains(contentBytes, []byte(oldPkg)) {
				newContent := bytes.ReplaceAll(contentBytes, []byte(oldPkg), []byte(settings.PackageName))
				_ = os.WriteFile(path, newContent, 0644)
			}
		}
		return nil
	})

	// 3. 更新所有 .smali 文件中的包名引用
	oldPkgSlash := strings.ReplaceAll(oldPkg, ".", "/")
	newPkgSlash := strings.ReplaceAll(settings.PackageName, ".", "/")
	oldPkgRef := "L" + oldPkgSlash + "/"
	newPkgRef := "L" + newPkgSlash + "/"

	err = filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".smali") {
			return nil
		}
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(contentBytes)
		// 替换类引用 Lcom/mediaplayer/app/ -> Lcom/haixin/tv/
		if strings.Contains(content, oldPkgRef) {
			content = strings.ReplaceAll(content, oldPkgRef, newPkgRef)
			// 也替换字符串中的完整包名（如 com.mediaplayer.app -> com.haixin.tv）
			content = strings.ReplaceAll(content, oldPkg, settings.PackageName)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return err
			}
		} else if strings.Contains(content, oldPkg) {
			// 仅字符串引用（无 L 前缀）
			content = strings.ReplaceAll(content, oldPkg, settings.PackageName)
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("遍历更新 smali 文件包名引用失败: %w", err)
	}

	// 4. 重命名目录结构
	// 遍历 smali/ smali_classes2/ 等目录，将旧包路径重命名为新包路径
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return fmt.Errorf("读取临时目录失败: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name != "smali" && !strings.HasPrefix(name, "smali_classes") {
			continue
		}
		oldDir := filepath.Join(tempDir, name, oldPkgSlash)
		newDir := filepath.Join(tempDir, name, newPkgSlash)
		if _, err := os.Stat(oldDir); os.IsNotExist(err) {
			continue
		}
		// 创建新目录结构
		if err := os.MkdirAll(newDir, 0755); err != nil {
			return fmt.Errorf("创建新包名目录 %s 失败: %w", newDir, err)
		}
		// 先收集所有文件，再统一复制（避免 walk 过程中删除文件导致遍历异常）
		var filesToMove []struct{ src, dst string }
		err = filepath.Walk(oldDir, func(srcPath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			relPath, _ := filepath.Rel(oldDir, srcPath)
			dstPath := filepath.Join(newDir, relPath)
			if info.IsDir() {
				return os.MkdirAll(dstPath, 0755)
			}
			filesToMove = append(filesToMove, struct{ src, dst string }{srcPath, dstPath})
			return nil
		})
		if err != nil {
			return fmt.Errorf("遍历包名目录 %s 失败: %w", oldDir, err)
		}
		// 复制所有文件
		for _, f := range filesToMove {
			data, err := os.ReadFile(f.src)
			if err != nil {
				return fmt.Errorf("读取文件 %s 失败: %w", f.src, err)
			}
			if err := os.WriteFile(f.dst, data, 0644); err != nil {
				return fmt.Errorf("写入文件 %s 失败: %w", f.dst, err)
			}
		}
		// 删除旧目录
		_ = os.RemoveAll(oldDir)
	}

	return nil
}
