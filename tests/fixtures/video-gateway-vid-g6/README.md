# VID-G6 本地合成媒体夹具

仅用于非商业关闭态测试，无人物、品牌、真实用户数据或Provider调用。`media-toolchain.json`锁定PyPI wheel、FFmpeg二进制及生成MP4的SHA-256；没有全局安装或修改业务依赖。生成脚本拒绝覆盖已有文件。

生成：通过锁定wheel取得FFmpeg7.1，将实际二进制路径传给`generate-playable.ps1 -FfmpegPath <路径>`。输出为5秒、1280×720、24fps、H.264、无音频的本地图样，4054453字节。FFmpeg解码到null成功、实际120帧；媒体时基12288与电影时基不同，不能靠改为相同时基掩盖探测器错误。

`service/video_playable_fixture_test.go`仅在测试构建嵌入MP4。测试Provider仍沿用Fake原生异步的提交、查询和成本确认，只替换受控OpenContent返回的本地媒体。原G5执行、归档、结算、交付及HTTP读取均真实执行。

`playable-preview.cjs`只监听127.0.0.1随机端口，只提供说明页和固定MP4；用于浏览器解码、拖动及响应式验证。它不是业务网关、不含Project SK，也不能作为鉴权端到端证据。浏览器使用本机已缓存的Playwright CLI0.1.18和独立命名会话；测试结束关闭该会话和本次静态服务，不影响其他任务。

原生npx启动CLI曾在输出帮助后触发Windows libuv退出断言；改用固定Node v24.19.0直接执行缓存CLI成功，没有改全局工具。浏览器仅有favicon.ico的404控制台记录，媒体error=null；不将该静态页称为生产页面验收。
