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
	logFunc("[INFO] 正在将应用名称修改为: %s", settings.AppName)
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
		logFunc("[INFO] 应用名称引用了资源: %s (键: %s)", labelValue, stringKey)
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
						logFunc("[INFO] 已成功更新资源文件: %s", path)
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
			logFunc("[WARN] 未在 strings.xml 中找到对应的键，自动降级在 AndroidManifest.xml 中替换硬编码值")
			newManifest := strings.Replace(manifestStr, `android:label="`+labelValue+`"`, `android:label="`+escapedName+`"`, 1)
			if err := os.WriteFile(manifestPath, []byte(newManifest), 0644); err != nil {
				return fmt.Errorf("写入 AndroidManifest.xml 失败: %w", err)
			}
		}
	} else {
		logFunc("[INFO] 发现应用名称是硬编码值: %s", labelValue)
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
	logFunc("[INFO] 正在修改版本名称为: %s, 版本号为: %d", settings.VersionName, settings.VersionCode)
	apktoolYmlPath := filepath.Join(tempDir, "apktool.yml")
	if _, err := os.Stat(apktoolYmlPath); os.IsNotExist(err) {
		logFunc("[WARN] 未找到 apktool.yml 文件，跳过版本修改")
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
	ymlStr = verCodeReg.ReplaceAllString(ymlStr, fmt.Sprintf("versionCode: '%s'", fmt.Sprintf("%d", settings.VersionCode)))

	// 【关键修复】：针对 targetSdkVersion >= 30 (Android 11) 的强制要求，
	// 确保 resources.arsc 存储时不进行压缩，以解决安装时 R+ Failure [-124] 的报错。
	if !strings.Contains(ymlStr, "- resources.arsc") {
		logFunc("[INFO] 正在将 resources.arsc 标记为不压缩，以适配 Android 11+ 系统规范")
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
	CustomLogoPath string
}

func (m *LogoAndBannerModifier) Name() string { return "LogoAndBannerModifier" }

func (m *LogoAndBannerModifier) Modify(ctx context.Context, tempDir string, settings *CustomSettings, logFunc func(string, ...interface{})) error {
	if m.CustomLogoPath == "" {
		return nil
	}
	if _, err := os.Stat(m.CustomLogoPath); os.IsNotExist(err) {
		logFunc("[WARN] 上传的 Logo 文件不存在: %s，跳过 Logo 替换", m.CustomLogoPath)
		return nil
	}

	logFunc("[INFO] 开始替换客户端应用图标与 TV 桌面横幅...")

	// 1. 替换所有的 ic_launcher.png 和 ic_launcher_foreground.png
	logoBytes, err := os.ReadFile(m.CustomLogoPath)
	if err != nil {
		return fmt.Errorf("读取定制 Logo 失败: %w", err)
	}

	resDir := filepath.Join(tempDir, "res")
	replacedIcons := 0
	err = filepath.Walk(resDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			name := info.Name()
			if name == "ic_launcher.png" || name == "ic_launcher_foreground.png" {
				if err := os.WriteFile(path, logoBytes, 0644); err != nil {
					return err
				}
				replacedIcons++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("遍历并替换图标失败: %w", err)
	}
	logFunc("[INFO] 成功替换了 %d 个应用图标文件(包含传统和自适应前景图标)", replacedIcons)

	// 2. 自动合成并替换 TV 横屏桌面横幅 (banner.png)
	logoFile, err := os.Open(m.CustomLogoPath)
	if err != nil {
		logFunc("[WARN] 无法打开定制 Logo 图像用于 Banner 合成: %v", err)
		return nil
	}
	defer logoFile.Close()

	logoImg, _, err := image.Decode(logoFile)
	if err != nil {
		logFunc("[WARN] 定制 Logo 图像解码失败，无法生成 TV 横幅: %v", err)
		return nil
	}

	// 合成 320x180 的横幅图片
	bannerWidth := 320
	bannerHeight := 180
	bannerImg := image.NewRGBA(image.Rect(0, 0, bannerWidth, bannerHeight))

	// 背景色填充：使用系统默认深蓝色 (#012576)
	bgColor := color.RGBA{R: 1, G: 37, B: 118, A: 255}
	draw.Draw(bannerImg, bannerImg.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

	// 计算 Logo 的等比缩放，使其高度保持在 120 像素左右，且不超出画布宽度
	targetHeight := 120
	origWidth := logoImg.Bounds().Dx()
	origHeight := logoImg.Bounds().Dy()

	targetWidth := int(float64(origWidth) * (float64(targetHeight) / float64(origHeight)))
	if targetWidth > 260 { // 宽度最大限制为 260
		targetWidth = 260
		targetHeight = int(float64(origHeight) * (float64(targetWidth) / float64(origWidth)))
	}

	// 进行简单的双线性/最近邻图像缩放
	scaledLogo := resizeImage(logoImg, targetWidth, targetHeight)

	// 计算居中坐标并进行绘制
	offsetX := (bannerWidth - targetWidth) / 2
	offsetY := (bannerHeight - targetHeight) / 2
	draw.Draw(bannerImg, image.Rect(offsetX, offsetY, offsetX+targetWidth, offsetY+targetHeight), scaledLogo, image.Point{}, draw.Over)

	// 保存并覆盖 res/drawable/banner.png
	bannerPath := filepath.Join(tempDir, "res", "drawable", "banner.png")
	// 确保父目录存在
	_ = os.MkdirAll(filepath.Dir(bannerPath), 0755)

	outBannerFile, err := os.OpenFile(bannerPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("创建 TV 横幅 banner.png 文件失败: %w", err)
	}
	defer outBannerFile.Close()

	if err := png.Encode(outBannerFile, bannerImg); err != nil {
		return fmt.Errorf("编码并写入 banner.png 失败: %w", err)
	}
	logFunc("[INFO] 成功自动缩放合成了 TV 宽屏桌面横幅: res/drawable/banner.png")

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
	logFunc("[INFO] 开始修改 Smali 配置文件以写入直连参数并隐藏二维码...")

	// 1. 查找并遍历所有的 smali 文件以定位 Prefs.smali
	var prefsSmaliPath string
	err := filepath.Walk(tempDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), "Prefs.smali") {
			// 二级确认：路径中包含 com/mediaplayer/app
			normalizedPath := filepath.ToSlash(path)
			if strings.Contains(normalizedPath, "com/mediaplayer/app") {
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

	logFunc("[INFO] 成功定位到 Prefs.smali 路径: %s", prefsSmaliPath)

	// 2. 替换服务器默认地址
	if settings.DefaultServerURL != "" {
		logFunc("[INFO] 正在将内置默认服务器地址修改为: %s", settings.DefaultServerURL)
		err = replaceSmaliConstant(prefsSmaliPath, "DEFAULT_SERVER_URL", settings.DefaultServerURL, logFunc)
		if err != nil {
			return fmt.Errorf("修改默认服务器地址失败: %w", err)
		}
	}

	// 3. 将 ALLOW_SERVER_CONFIG 设为 false，以隐藏二维码页面
	logFunc("[INFO] 正在将 ALLOW_SERVER_CONFIG 置为 false (隐藏配置二维码)")
	err = replaceSmaliConstant(prefsSmaliPath, "ALLOW_SERVER_CONFIG", false, logFunc)
	if err != nil {
		return fmt.Errorf("修改 ALLOW_SERVER_CONFIG 常量失败: %w", err)
	}

	// 4. 作为补充保障，递归扫描并替换 smali 代码中所有的 http://0.0.0.0:9527 占位符地址
	if settings.DefaultServerURL != "" {
		logFunc("[INFO] 正在替换 Smali 代码中所有 http://0.0.0.0:9527 硬编码地址占位符...")
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
		logFunc("[INFO] 完成硬编码地址替换，共更新了 %d 个 smali 文件", replacedCount)
	}

	return nil
}

// replaceSmaliConstant 定位并修改 Smali 文件中的静态常量字段
func replaceSmaliConstant(filePath, fieldName string, newValue interface{}, logFunc func(string, ...interface{})) error {
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	content := string(contentBytes)
	lines := strings.Split(content, "\n")
	modified := false

	// 正则匹配 smali 中静态字段的定义
	// 例如：
	// .field public static final ALLOW_SERVER_CONFIG:Z = 0x1
	// .field public static final DEFAULT_SERVER_URL:Ljava/lang/String; = "http://0.0.0.0:9527"
	for i, line := range lines {
		if strings.Contains(line, fieldName) && strings.Contains(line, ".field") && strings.Contains(line, "static") {
			switch val := newValue.(type) {
			case bool:
				// 替换布尔型常量（兼容 0x1/0x0 以及 true/false）
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:Z\s*=\s*)(true|false|0x[0-9a-fA-F]+|[0-9]+)`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					replacement := "false"
					if val {
						replacement = "true"
					}
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf("${1}%s", replacement))
					modified = true
					logFunc("[INFO] 已成功更新 Smali 布尔型字段 %s -> %v", fieldName, val)
				}
			case string:
				// 替换字符串型常量
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:Ljava/lang/String;\s*=\s*)".*"`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf(`${1}"%s"`, val))
					modified = true
					logFunc("[INFO] 已成功更新 Smali 字符串型字段 %s -> %s", fieldName, val)
				}
			case int:
				// 替换整型常量
				reg := regexp.MustCompile(fmt.Sprintf(`(%s:I\s*=\s*)(0x[0-9a-fA-F]+|[0-9\-]+)`, regexp.QuoteMeta(fieldName)))
				if reg.MatchString(line) {
					lines[i] = reg.ReplaceAllString(line, fmt.Sprintf(`${1}0x%x`, val))
					modified = true
					logFunc("[INFO] 已成功更新 Smali 整型字段 %s -> %v", fieldName, val)
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

	logFunc("[EXEC] 正在执行: %s %s", cmdName, strings.Join(args, " "))
	err := cmd.Run()
	
	// 输出子进程的常规日志
	if stdoutBuf.Len() > 0 {
		outLines := strings.Split(stdoutBuf.String(), "\n")
		for _, line := range outLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				logFunc("[TOOL] %s", trimmed)
			}
		}
	}

	if err != nil {
		// 精确提炼 Stderr 中的具体报错并输出
		if stderrBuf.Len() > 0 {
			errLines := strings.Split(stderrBuf.String(), "\n")
			logFunc("[ERROR] 工具底层报错输出:")
			for _, line := range errLines {
				trimmed := strings.TrimSpace(line)
				if trimmed != "" {
					logFunc("[ERROR-TOOL] %s", trimmed)
				}
			}
		}
		return fmt.Errorf("命令执行失败 %s: %w", cmdName, err)
	}
	return nil
}
