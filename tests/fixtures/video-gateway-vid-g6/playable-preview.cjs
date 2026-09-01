// 临时媒体兼容页仅监听回环，固定两个资源，不暴露项目目录、Key或业务接口。
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const media = path.resolve(__dirname, '../../../server/internal/modules/token_gateway/service/testdata/vid_g6_playable.mp4');
const size = fs.statSync(media).size;
const html = `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>VID-G6 本地合成媒体验证</title><style>body{font:16px system-ui;margin:24px auto;padding:0 16px;max-width:900px}video,input{display:block;width:100%;max-width:900px}button{padding:12px;margin:12px 0}</style><h1>本地合成媒体验证</h1><p>仅验证合成MP4的浏览器解码与拖动，不代表网关鉴权端到端验收。</p><video id="video" controls muted preload="auto" src="/fixture.mp4"></video><button id="play">播放 / 暂停</button><label for="seek">拖动播放位置（0—4秒）</label><input id="seek" aria-label="播放位置" type="range" min="0" max="4" value="0" step="0.1"><output id="status">加载中</output><script>const v=document.getElementById('video'),s=document.getElementById('seek'),o=document.getElementById('status');document.getElementById('play').onclick=()=>v.paused?v.play():v.pause();s.oninput=()=>{v.currentTime=Number(s.value)};v.onloadedmetadata=()=>{o.value='已加载 '+v.duration+' 秒'};v.onseeked=()=>{o.value='已跳转 '+v.currentTime.toFixed(2)+' 秒'};</script></html>`;
const server = http.createServer((req, res) => {
  res.setHeader('Cache-Control', 'no-store');
  if (req.method !== 'GET') { res.writeHead(405); return res.end(); }
  if (req.url === '/') { res.setHeader('Content-Type', 'text/html;charset=utf-8'); return res.end(html); }
  if (req.url !== '/fixture.mp4') { res.writeHead(404); return res.end(); }
  let start=0,end=size-1,status=200;
  if (req.headers.range) {
    const match=/^bytes=(\d+)-(\d*)$/.exec(req.headers.range);
    if (!match || Number(match[1]) >= size) { res.writeHead(416,{'Content-Range':'bytes */'+size});return res.end(); }
    start=Number(match[1]);end=match[2]?Math.min(Number(match[2]),end):end;status=206;
    if(end<start){res.writeHead(416,{'Content-Range':'bytes */'+size});return res.end();}
    res.setHeader('Content-Range', 'bytes '+start+'-'+end+'/'+size);
  }
  res.writeHead(status,{'Content-Type':'video/mp4','Content-Length':end-start+1,'Accept-Ranges':'bytes'});
  const stream=fs.createReadStream(media,{start,end});res.on('close',()=>stream.destroy());stream.pipe(res);
});
server.listen(0,'127.0.0.1',()=>process.stdout.write('VID_G6_MEDIA_PREVIEW=http://127.0.0.1:'+server.address().port+'\n'));
