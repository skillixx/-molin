// 使用官方TypeScript/Node SDK；只有显式execute才发送真实loopback HTTP。
import OpenAI, { toFile } from 'openai';
import { readFileSync, realpathSync, statSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, resolve, relative, isAbsolute, extname } from 'node:path';
import { Agent, request as httpRequest } from 'node:http';
import { Readable } from 'node:stream';
import { createHash } from 'node:crypto';

const requireModule = createRequire(import.meta.url);
function check(ok: unknown, code: string): asserts ok { if (!ok) throw new Error(code); }
const jobFields = ['id', 'completed_at', 'created_at', 'error', 'expires_at', 'model', 'object', 'progress', 'prompt', 'remixed_from_video_id', 'seconds', 'size', 'status'].sort();
function job(value: any, expectedModel: string) {
  check(JSON.stringify(Object.keys(value).sort()) === JSON.stringify(jobFields), 'JOB_FIELDS');
  check(/^video_[A-Za-z0-9_-]{8,64}$/.test(value.id), 'PUBLIC_ID');
  check(value.object === 'video' && value.seconds === '5' && value.size === '1280x720', 'JOB_SPEC');
  check(typeof value.model === 'string' && value.model.length > 0 && value.model.length <= 128 && value.model === expectedModel, 'JOB_MODEL');
  check(value.prompt === null && value.remixed_from_video_id === null, 'JOB_NULLS');
  check(['queued', 'in_progress', 'completed', 'failed'].includes(value.status), 'JOB_STATUS');
  check(Number.isInteger(value.created_at) && typeof value.progress === 'number' && value.progress >= 0 && value.progress <= 100, 'JOB_TYPES');
  for (const key of ['completed_at', 'expires_at']) check(value[key] === null || Number.isInteger(value[key]), 'JOB_TIME');
  if (value.error !== null) {
    check(JSON.stringify(Object.keys(value.error).sort()) === JSON.stringify(['code', 'message']), 'JOB_ERROR');
    check(typeof value.error.code === 'string' && value.error.code.length > 0 && value.error.code.length <= 64 && typeof value.error.message === 'string' && value.error.message.length > 0 && value.error.message.length <= 512, 'JOB_ERROR_TYPES');
  }
}
function pageShape(page: any, model: string) {
  check(JSON.stringify(Object.keys(page).sort()) === JSON.stringify(['data', 'first_id', 'has_more', 'last_id', 'object']) && page.object === 'list' && typeof page.has_more === 'boolean' && Array.isArray(page.data) && page.data.length <= 100, 'LIST_SHAPE');
  page.data.forEach((value: any) => job(value, model));
  check(page.first_id === (page.data.length ? page.data[0].id : null) && page.last_id === (page.data.length ? page.data.at(-1).id : null), 'LIST_CURSOR');
}

async function main() {
  // 官方包不导出package.json子路径；从已解析SDK入口定位真实安装元数据。
  const installed = JSON.parse(readFileSync(resolve(dirname(requireModule.resolve('openai')), 'package.json'), 'utf8'));
  check(installed.version === '6.39.0', 'SDK_VERSION_MISMATCH');
  if (!process.argv.includes('--execute')) {
    console.log(JSON.stringify({ dependency_check: 'PASS', http_contract: 'NOT_RUN', sdk: 'openai@6.39.0' }));
    return;
  }
  check(process.env.VID_G6_SDK_APPROVED === 'ISOLATED_SYNTHETIC_ONLY', 'EXECUTION_AUTH_REQUIRED');
  const at = process.argv.indexOf('--fixture');
  check(at > 0 && process.argv[at + 1], 'FIXTURE_REQUIRED');
  const fixturePath = realpathSync(process.argv[at + 1]);
  const fixture = JSON.parse(readFileSync(fixturePath, 'utf8'));
  const origin = new URL(fixture.origin);
  check(origin.protocol === 'http:' && ['127.0.0.1', '[::1]'].includes(origin.hostname) && origin.port &&
    !origin.username && !origin.password && origin.pathname === '/' && !origin.search && !origin.hash &&
    fixture.origin === origin.origin, 'LOOPBACK_ORIGIN_REQUIRED');
  check(fixture.purpose === 'isolated_synthetic_fixture' && fixture.disposable === true && /^[a-z0-9_-]{8,40}$/.test(fixture.run_id), 'FIXTURE_SCOPE');
  check(/^[A-Za-z0-9._/-]{1,128}$/.test(fixture.model), 'MODEL');
  const c = fixture.typescript;
  check(/^video_[A-Za-z0-9_-]{8,64}$/.test(c.completed_video_id) && /^[A-Za-z0-9_-]{8,128}$/.test(c.request_id), 'FIXTURE_IDS');
  check(Number.isInteger(c.media_size_bytes) && c.media_size_bytes >= 16 && c.media_size_bytes <= 8 * 1024 * 1024 && /^[0-9a-f]{64}$/.test(c.media_sha256), 'MEDIA_FIXTURE');
  check(['request_id', 'quote_id', 'billing_status', 'settled_amount'].every(key => Object.hasOwn(c.billing_facts, key)) &&
    c.billing_facts.request_id === c.request_id && c.billing_facts.billing_status === 'settled', 'BILLING_FIXTURE');
  const image = realpathSync(resolve(dirname(fixturePath), fixture.reference_image));
  const imageRelative = relative(dirname(fixturePath), image);
  check(imageRelative !== '..' && !imageRelative.startsWith('..' + '/') && !imageRelative.startsWith('..\\') && !isAbsolute(imageRelative) &&
    ['.png', '.jpg', '.jpeg'].includes(extname(image).toLowerCase()) && statSync(image).size > 0 && statSync(image).size <= 10 * 1024 * 1024, 'LOCAL_REFERENCE_REQUIRED');
  const key = process.env.VID_G6_SDK_SK ?? '';
  check(/^sk-molin-g6-fixture-[A-Za-z0-9_-]{16,128}$/.test(key), 'SYNTHETIC_SK_REQUIRED');
  // Node24全局Agent可被--use-env-proxy改变；专用Agent必须显式覆盖代理环境。
  const directAgent = new Agent({ keepAlive: false, proxyEnv: {} });

  // 原生HTTP请求不使用环境代理；仍把真实网络Response交回SDK解析，绝非Mock服务。
  const localFetch: typeof fetch = async (input, init) => {
    const req = new Request(input, init);
    const target = new URL(req.url);
    check(target.origin === origin.origin && (target.pathname.startsWith('/v1/videos') || target.pathname.startsWith('/api/token/videos/requests/')), 'OUTBOUND_BLOCKED');
    const data = req.body ? Buffer.from(await req.arrayBuffer()) : undefined;
    check(!data || data.length <= 11 * 1024 * 1024, 'REQUEST_BOUND');
    return await new Promise<Response>((resolveResponse, reject) => {
      const outgoing = httpRequest(target, { agent: directAgent, method: req.method, headers: Object.fromEntries(req.headers), signal: req.signal }, incoming => {
        const status = incoming.statusCode ?? 500;
        if (status >= 300 && status < 400) { incoming.destroy(); reject(new Error('REDIRECT_BLOCKED')); return; }
        const headers = new Headers();
        for (const [name, value] of Object.entries(incoming.headers)) if (value !== undefined) headers.set(name, Array.isArray(value) ? value.join(', ') : value);
        const stream = Readable.toWeb(incoming) as ReadableStream<Uint8Array>;
        resolveResponse(new Response(stream, { status, headers }));
      });
      outgoing.setTimeout(15000, () => outgoing.destroy(new Error('HTTP_TIMEOUT')));
      outgoing.on('error', reject);
      outgoing.end(data);
    });
  };
  const client = new OpenAI({ apiKey: key, baseURL: origin.origin + '/v1', fetch: localFetch, maxRetries: 0, timeout: 15000, logLevel: 'off' });
  const report: any = { sdk: 'openai@6.39.0', http_contract: 'FAIL', cases: [] };
  let current = 'preflight';
  const headers = (suffix: string) => ({ 'Idempotency-Key': 'vid-g6-ts-' + fixture.run_id + '-' + suffix });
  const passed = () => report.cases.push({ case: current, status: 'PASS' });
  try {
    // 先读原始JSON，再await同一SDK Promise；响应只请求一次，字段缺失不会被SDK默认值遮蔽。
    async function sdkJSON(promise: any) {
      const response: Response = await promise.asResponse();
      check(response.status === 200 && response.headers.get('x-request-id'), 'HTTP_200_TRACE');
      const value = await response.clone().json();
      await promise;
      return { value, response };
    }
    current = 'create_t2v_and_replay';
    // 5秒及Molin模型是明确的兼容差异；只适配SDK类型，不替换为官方4/8/12秒。
    const params = { model: fixture.model, prompt: '合成SDK文生视频测试', seconds: '5' as never, size: '1280x720' as const };
    try { await client.videos.create(params); throw new Error('IDEMPOTENCY_MUST_BE_REQUIRED'); }
    catch (error) { check(error instanceof OpenAI.APIError && error.status === 400, 'MISSING_IDEMPOTENCY_400'); }
    const created = await sdkJSON(client.videos.create(params, { headers: headers('t2v') }));
    job(created.value, fixture.model);
    const business = created.response.headers.get('x-molin-request-id');
    check(business && business !== created.response.headers.get('x-request-id'), 'BUSINESS_TRACE');
    const replay = await sdkJSON(client.videos.create(params, { headers: headers('t2v') }));
    job(replay.value, fixture.model);
    check(replay.value.id === created.value.id && replay.response.headers.get('x-molin-request-id') === business, 'REPLAY_IDENTITY');
    try { await client.videos.create({ ...params, prompt: '另一个合成生成意图' }, { headers: headers('t2v') }); throw new Error('CONFLICT_MUST_FAIL'); }
    catch (error) { check(error instanceof OpenAI.APIError && error.status === 409, 'IDEMPOTENCY_409'); }
    passed();
    current = 'create_i2v_multipart';
    const reference = await toFile(readFileSync(image), 'reference' + extname(image), { type: extname(image).toLowerCase() === '.png' ? 'image/png' : 'image/jpeg' });
    const i2v = await sdkJSON(client.videos.create({ ...params, prompt: '合成SDK图生视频测试', input_reference: reference }, { headers: headers('i2v') }));
    job(i2v.value, fixture.model);
    const i2vBusiness = i2v.response.headers.get('x-molin-request-id');
    check(i2vBusiness && /^[A-Za-z0-9._:-]{8,128}$/.test(i2vBusiness) && i2vBusiness !== i2v.response.headers.get('x-request-id'), 'I2V_BUSINESS_TRACE');
    const i2vReplay = await sdkJSON(client.videos.create({ ...params, prompt: '合成SDK图生视频测试', input_reference: reference }, { headers: headers('i2v') }));
    job(i2vReplay.value, fixture.model);
    check(i2vReplay.value.id === i2v.value.id && i2vReplay.response.headers.get('x-molin-request-id') === i2v.response.headers.get('x-molin-request-id'), 'I2V_REPLAY');
    check(i2vReplay.response.headers.get('x-molin-request-id') !== i2vReplay.response.headers.get('x-request-id'), 'I2V_REPLAY_TRACE');
    passed();
    current = 'retrieve_and_list';
    const retrieved = await sdkJSON(client.videos.retrieve(created.value.id));
    job(retrieved.value, fixture.model);
    check(retrieved.value.id === created.value.id, 'RETRIEVE_ID');
    const page = (await sdkJSON(client.videos.list({ limit: 100, order: 'desc' }))).value;
    pageShape(page, fixture.model);
    check(page.data.length <= 100 && page.data.some((value: any) => value.id === created.value.id), 'LIST_CREATED');
    check(page.has_more === false && page.data.some((value: any) => value.id === c.completed_video_id), 'SINGLE_PAGE_DELETE_FIXTURE_REQUIRED');
    passed();
    current = 'completed_content_and_ranges';
    const completed = (await sdkJSON(client.videos.retrieve(c.completed_video_id))).value;
    job(completed, fixture.model);
    check(completed.status === 'completed', 'COMPLETED_FIXTURE_REQUIRED');
    const etag = '"' + c.media_sha256 + '"';
    for (const [range, validator, status, size] of [[null, null, 200, c.media_size_bytes], ['bytes=0-15', etag, 206, 16], ['bytes=0-15', '"old-fixture"', 200, c.media_size_bytes]] as const) {
      const response = await client.videos.downloadContent(c.completed_video_id, {}, { headers: range ? { Range: range, 'If-Range': validator! } : {} });
      check(response.status === status && response.headers.get('etag') === etag && response.headers.get('content-type') === 'video/mp4' && response.headers.get('accept-ranges') === 'bytes', 'CONTENT_HEADERS');
      check(Number(response.headers.get('content-length')) === size, 'CONTENT_LENGTH');
      if (status === 206) check(response.headers.get('content-range') === 'bytes 0-15/' + c.media_size_bytes, 'CONTENT_RANGE');
      check(response.body, 'CONTENT_BODY');
      const reader = response.body.getReader();
      const hash = createHash('sha256');
      let total = 0;
      let prefix = Buffer.alloc(0);
      try {
        while (true) {
          const next = await reader.read();
          if (next.done) break;
          total += next.value.length;
          check(total <= 8 * 1024 * 1024, 'MEDIA_BOUND');
          prefix = Buffer.concat([prefix, Buffer.from(next.value).subarray(0, 16)]).subarray(0, 16);
          hash.update(next.value);
        }
      } finally { await reader.cancel(); reader.releaseLock(); }
      check(total === size && prefix.subarray(4, 8).toString('ascii') === 'ftyp', 'MP4_BYTES');
      if (status === 200) check(hash.digest('hex') === c.media_sha256, 'MEDIA_HASH');
    }
    for (const range of ['bytes=-', 'bytes=0-1,4-5', 'bytes=' + c.media_size_bytes + '-']) {
      try { await client.videos.downloadContent(c.completed_video_id, {}, { headers: { Range: range } }); throw new Error('RANGE_MUST_FAIL'); }
      catch (error) { check(error instanceof OpenAI.APIError && error.status === 416 && error.headers?.get('content-range') === 'bytes */' + c.media_size_bytes, 'RANGE_416'); }
    }
    passed();
    current = 'delete_and_retained_billing';
    async function billing(deleted: boolean) {
      for (const suffix of [c.request_id, 'by-video/' + c.completed_video_id]) {
        const response = await localFetch(origin.origin + '/api/token/videos/requests/' + suffix, { headers: { Authorization: 'Bearer ' + key } });
        check(response.status === 200, 'BILLING_STATUS');
        const envelope = await response.json();
        check(envelope.code === 0 && envelope.data.media_deleted === deleted, 'BILLING_ENVELOPE');
        for (const [field, value] of Object.entries(c.billing_facts)) check(JSON.stringify(envelope.data[field]) === JSON.stringify(value), 'BILLING_FACT_RETAINED');
      }
    }
    await billing(false);
    const removed = (await sdkJSON(client.videos.delete(c.completed_video_id, { headers: headers('delete') }))).value;
    check(Object.keys(removed).length === 3 && removed.id === c.completed_video_id && removed.object === 'video.deleted' && removed.deleted === true, 'DELETE_SHAPE');
    for (const operation of [() => client.videos.retrieve(c.completed_video_id), () => client.videos.downloadContent(c.completed_video_id)]) {
      try { await operation(); throw new Error('DELETED_MUST_BE_404'); }
      catch (error) { check(error instanceof OpenAI.APIError && error.status === 404, 'DELETED_404'); }
    }
    const after = (await sdkJSON(client.videos.list({ limit: 100, order: 'desc' }))).value;
    pageShape(after, fixture.model);
    check(after.has_more === false && after.data.every((value: any) => value.id !== c.completed_video_id), 'DELETED_LIST_HIDDEN');
    await billing(true);
    passed();
    report.http_contract = 'PASS';
  } catch (error) {
    report.cases.push({ case: current, status: 'FAIL', error_class: error instanceof Error ? error.constructor.name : 'Unknown', http_status: error instanceof OpenAI.APIError ? error.status : null });
  } finally { directAgent.destroy(); }
  console.log(JSON.stringify(report));
  if (report.http_contract !== 'PASS') process.exitCode = 1;
}

// 不输出异常正文，避免SDK错误对象带出响应、请求体或鉴权材料。
main().catch(error => { console.log(JSON.stringify({ status: 'NOT_RUN_OR_FAILED', error_class: error instanceof Error ? error.constructor.name : 'Unknown' })); process.exitCode = 1; });
