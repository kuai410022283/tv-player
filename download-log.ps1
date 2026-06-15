$url = "http://nas.laokhome.cn:8888/e9ccac865502b310.log"
$out = "latest.log"
Invoke-WebRequest -Uri $url -OutFile $out -UseBasicParsing
Write-Host "下载完成: $(Resolve-Path $out)"
