const MODEL_MAP = {
  "doubao-seedance-2.0": "seedance-2.0",
  "doubao-seedance-2.0-fast": "seedance-2.0-fast",
  "doubao-seedance-2.0-mini": "seedance-2.0-mini",
  "doubao-seedance-2.0-face": "seedance-2.0-face",
  "doubao-seedance-2.0-fast-face": "seedance-2.0-fast-face",
  "doubao-seedance-2.5": "seedance-2.5",
};

const MODELS = Object.keys(MODEL_MAP);
const REFERENCE_ERROR = "aivideoapi v1 supports text-to-video only; reference image/video inputs are not supported";

export const meta = {
  apiVersion: 1,
  key: "aivideoapi",
  name: "Aivideo API Seedance",
  icon: "Doubao.Color",
  description: {
    en: "Aivideo API Seedance text-to-video generation",
    zh: "Aivideo API Seedance 文生视频",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  models: MODELS,
  fetchMode: "per_task",
  usageSchema: {
    seconds: {
      type: "number",
      unit: "second",
      description: { en: "Requested or upstream video duration in seconds.", zh: "请求或上游返回的视频时长（秒）。" },
    },
    resolution: {
      enum: ["480p", "720p", "1080p", "4k"],
      description: { en: "Output video resolution tier.", zh: "输出视频分辨率档位。" },
    },
    video_input: {
      enum: ["none"],
      description: { en: "Aivideo API v1 rejects reference image and video inputs.", zh: "Aivideo API v1 拒绝参考图片和视频输入。" },
    },
  },
  usageExamples: [
    { label: "720p · 5s", facts: { seconds: 5, resolution: "720p", video_input: "none" } },
    { label: "1080p · 10s", facts: { seconds: 10, resolution: "1080p", video_input: "none" } },
  ],
  routes: [
    { method: "POST", path: "/v1/videos/generations", type: "submit", decode: "createTask", render: "taskCreated", models: MODELS },
    { method: "GET", path: "/v1/tasks/:task_id", type: "query", render: "taskStatus" },
  ],
  protocols: ["openai_video"],
  auth: "api_key",
};

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function trimmed(value) {
  return String(value === undefined || value === null ? "" : value).trim();
}

function upstreamModel(model) {
  const value = trimmed(model);
  return MODEL_MAP[value] || value;
}

function rejectReference(value) {
  if (value !== undefined && value !== null && value !== "" && (!Array.isArray(value) || value.length > 0)) throw new Error(REFERENCE_ERROR);
}

function textFromContent(content) {
  if (!Array.isArray(content)) return "";
  const texts = [];
  for (const item of content) {
    if (typeof item === "string") {
      if (trimmed(item)) texts.push(item);
      continue;
    }
    if (!isObject(item)) continue;
    const type = trimmed(item.type).toLowerCase();
    if (type && type !== "text" && type !== "input_text") throw new Error(REFERENCE_ERROR);
    if (item.image_url !== undefined || item.video_url !== undefined || item.image_urls !== undefined || item.video_urls !== undefined || item.url !== undefined)
      throw new Error(REFERENCE_ERROR);
    if (typeof item.text === "string" && trimmed(item.text)) texts.push(item.text);
  }
  return texts.join("\n");
}

function textFromInput(input) {
  if (typeof input === "string") return input;
  if (!Array.isArray(input)) return "";
  const texts = [];
  for (const item of input) {
    if (typeof item === "string") {
      if (trimmed(item)) texts.push(item);
      continue;
    }
    if (!isObject(item)) continue;
    if (item.content !== undefined) {
      if (Array.isArray(item.content)) texts.push(textFromContent(item.content));
      else if (typeof item.content === "string") texts.push(item.content);
      else throw new Error(REFERENCE_ERROR);
      continue;
    }
    const type = trimmed(item.type).toLowerCase();
    if (type !== "text" && type !== "input_text") throw new Error(REFERENCE_ERROR);
    if (item.image_url !== undefined || item.video_url !== undefined || item.url !== undefined) throw new Error(REFERENCE_ERROR);
    if (typeof item.text === "string" && trimmed(item.text)) texts.push(item.text);
  }
  return texts.filter(function (text) { return trimmed(text); }).join("\n");
}

function rejectRequestReferences(req) {
  if (!isObject(req)) return;
  for (const key of [
    "image", "images", "image_url", "image_urls", "input_image", "input_images", "input_reference",
    "video", "videos", "video_url", "video_urls", "input_video", "reference", "references",
    "first_frame_image", "last_frame_image", "image_with_roles",
  ]) rejectReference(req[key]);
  if (req.content !== undefined && Array.isArray(req.content)) textFromContent(req.content);
  if (req.input !== undefined) {
    if (Array.isArray(req.input)) textFromInput(req.input);
    else if (isObject(req.input)) rejectRequestReferences(req.input);
  }
}

function normalizeResolution(value) {
  const raw = trimmed(value).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(raw)) return raw;
  const parts = raw.replace("*", "x").split("x");
  if (parts.length === 2) {
    const width = Number(parts[0]);
    const height = Number(parts[1]);
    if (Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0) {
      const max = Math.max(width, height);
      if (max >= 3840) return "4k";
      if (max >= 1920) return "1080p";
      if (max >= 1280) return "720p";
      return "480p";
    }
  }
  return "720p";
}

function normalizeDuration(value) {
  const duration = Number(value);
  if (!Number.isFinite(duration) || duration <= 0 || duration > 3600) throw new Error("duration must be between 1 and 3600 seconds");
  return duration;
}

function requestPrompt(req) {
  const texts = [];
  if (typeof req.prompt === "string" && trimmed(req.prompt)) texts.push(req.prompt);
  if (typeof req.text === "string" && trimmed(req.text)) texts.push(req.text);
  if (typeof req.content === "string" && trimmed(req.content)) texts.push(req.content);
  if (Array.isArray(req.content)) texts.push(textFromContent(req.content));
  if (req.input !== undefined) {
    if (typeof req.input === "string" || Array.isArray(req.input)) texts.push(textFromInput(req.input));
    else if (isObject(req.input)) {
      if (typeof req.input.prompt === "string" && trimmed(req.input.prompt)) texts.push(req.input.prompt);
      if (typeof req.input.text === "string" && trimmed(req.input.text)) texts.push(req.input.text);
      if (Array.isArray(req.input.content)) texts.push(textFromContent(req.input.content));
    }
  }
  return texts.filter(function (text) { return trimmed(text); }).join("\n");
}

function normalizedRequest(req, model) {
  if (!isObject(req)) throw new Error("request body must be an object");
  rejectRequestReferences(req);
  const prompt = requestPrompt(req);
  if (!trimmed(prompt)) throw new Error("text prompt is required");
  const output = { model: model, prompt: prompt };
  const durationValue = req.duration === undefined ? req.seconds : req.duration;
  output.duration = durationValue === undefined ? 5 : normalizeDuration(durationValue);
  output.resolution = normalizeResolution(req.resolution === undefined ? req.quality : req.resolution);
  const size = req.size === undefined ? req.aspect_ratio : req.size;
  output.size = size === undefined || size === null || size === "" ? "16:9" : size;
  for (const key of ["seed", "generate_audio", "return_last_frame", "negative_prompt", "watermark", "nsfw_check", "tools"]) {
    if (req[key] !== undefined && req[key] !== null && req[key] !== "") output[key] = req[key];
  }
  return output;
}

export const native = {
  createTask: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const body = ctx.body.value;
    if (!isObject(body)) throw new Error("request body must be an object");
    const model = trimmed(body.model);
    if (!model) throw new Error("model is required");
    const requestBody = normalizedRequest(body, model);
    return { kind: "submit", model: model, action: "text_to_video", requestBody: requestBody };
  },
  taskCreated: function (ctx, task) {
    const data = isObject(task && task.data) ? task.data : {};
    return Object.assign({}, data, { id: task.task_id });
  },
  taskStatus: function (ctx, task) {
    const data = isObject(task && task.data) ? task.data : {};
    const statusMap = { NOT_START: "pending", SUBMITTED: "pending", QUEUED: "pending", IN_PROGRESS: "processing", SUCCESS: "completed", FAILURE: "failed" };
    const status = statusMap[task.status] || "processing";
    const output = Object.assign({}, data, { id: task.task_id, status: status });
    if (task.fail_reason) output.error = { message: task.fail_reason };
    return output;
  },
  error: function (ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};

export function buildSubmitRequest(ctx) {
  const req = isObject(ctx && ctx.requestBody) ? ctx.requestBody : {};
  const request = normalizedRequest(req, upstreamModel((ctx && ctx.upstreamModel) || (ctx && ctx.model) || req.model));
  const input = {};
  for (const key of Object.keys(request)) input[key] = request[key];
  input.model = upstreamModel(input.model);
  return {
    url: ctx.baseUrl + "/v1/videos/generations",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: { input: input },
    action: "text_to_video",
    rewriteModel: input.model,
  };
}

function responseData(body) {
  return isObject(body && body.data) ? body.data : {};
}

function responseTaskId(body) {
  const data = responseData(body);
  return trimmed(data.taskId || data.task_id || data.id || (body && (body.taskId || body.task_id || body.id)));
}

export function parseSubmitResponse(ctx, response) {
  const body = response && response.body;
  const taskId = responseTaskId(body);
  if (!taskId) throw new Error("task id is empty");
  return { taskId: taskId, taskData: body };
}

function taskPayload(body) {
  const data = responseData(body);
  return isObject(data.output) ? data : (isObject(body) && isObject(body.output) ? body : data);
}

function videoURL(body) {
  const payload = taskPayload(body);
  const output = isObject(payload.output) ? payload.output : {};
  const candidates = [output.video_url, output.videoUrl, output.url, payload.video_url, payload.videoUrl, payload.url];
  for (const candidate of candidates) {
    if (trimmed(candidate)) return trimmed(candidate);
  }
  if (Array.isArray(output.videos)) {
    for (const video of output.videos) {
      const url = typeof video === "string" ? video : isObject(video) ? video.video_url || video.videoUrl || video.url : "";
      if (trimmed(url)) return trimmed(url);
    }
  }
  return "";
}

export function extractUsage(ctx) {
  if (ctx && ctx.usagePurpose === "billing_ratios") return null;
  const req = isObject(ctx && ctx.requestBody) ? ctx.requestBody : {};
  let seconds = req.duration === undefined ? req.seconds : req.duration;
  seconds = seconds === undefined ? 5 : Number(seconds);
  if (!Number.isFinite(seconds) || seconds <= 0) seconds = 5;
  return { seconds: Math.min(seconds, 3600), resolution: normalizeResolution(req.resolution === undefined ? req.quality : req.resolution), video_input: "none" };
}

export function extractUsageOnSubmit(ctx, _taskData) {
  return extractUsage(ctx);
}

export function extractUsageOnComplete(task, result, body) {
  const payload = taskPayload(body);
  const usage = isObject(payload.usage) ? payload.usage : isObject(body && body.usage) ? body.usage : {};
  const seconds = Number(usage.seconds || usage.duration || payload.duration);
  const facts = {};
  if (Number.isFinite(seconds) && seconds > 0) facts.seconds = seconds;
  const resolution = trimmed(payload.resolution || usage.resolution).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(resolution)) facts.resolution = resolution;
  if (Object.keys(facts).length === 0) return null;
  return facts;
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl + "/v1/tasks/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  const data = responseData(body);
  const status = trimmed(data.status || (body && body.status)).toLowerCase();
  const progressValue = Number(data.progress === undefined ? body && body.progress : data.progress);
  const progress = Number.isFinite(progressValue) ? Math.max(0, Math.min(100, progressValue)) + "%" : "";
  if (status === "pending" || status === "queued" || status === "submitted") return { status: "QUEUED", progress: progress || "10%" };
  if (status === "processing" || status === "running") return { status: "IN_PROGRESS", progress: progress || "50%" };
  if (status === "completed" || status === "succeeded" || status === "success") {
    const result = { status: "SUCCESS", progress: "100%" };
    const url = videoURL(body);
    if (url) result.url = url;
    return result;
  }
  if (status === "failed" || status === "error" || status === "cancelled") {
    const error = isObject(data.error) ? data.error : isObject(body && body.error) ? body.error : {};
    return { status: "FAILURE", progress: "100%", reason: trimmed(error.message || data.reason || data.fail_reason || status) };
  }
  return { status: "UNKNOWN", progress: progress || "0%", reason: status ? "unknown task status: " + status : "task status is missing" };
}

export function listArtifacts(task) {
  if (!task || task.status !== "SUCCESS") return [];
  return videoURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (!ctx || ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = videoURL(ctx.data);
  if (!url) throw new Error("artifact_not_found");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}

function parseMultipart(ctx) {
  if ((ctx.body.files || []).length) throw new Error(REFERENCE_ERROR);
  const fields = ctx.body.fields || {};
  const request = {};
  for (const name of Object.keys(fields)) {
    const values = fields[name] || [];
    if (values.length > 1) throw new Error(name + " must be provided once");
    request[name] = values[0];
  }
  if (request.metadata !== undefined) {
    let metadata;
    try { metadata = JSON.parse(request.metadata); } catch (e) { throw new Error("metadata must be a JSON object string"); }
    if (!isObject(metadata)) throw new Error("metadata must be a JSON object string");
    delete request.metadata;
    Object.assign(request, metadata);
  }
  if (request.duration !== undefined) request.duration = Number(request.duration);
  if (request.seconds !== undefined) request.seconds = Number(request.seconds);
  if (request.generate_audio !== undefined) request.generate_audio = request.generate_audio === true || request.generate_audio === "true";
  return request;
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      const req = ctx.body.kind === "json" ? ctx.body.value : parseMultipart(ctx);
      if (!isObject(req)) throw new Error("JSON object required");
      const model = trimmed(ctx.model || req.model);
      if (!model) throw new Error("model is required");
      const requestBody = normalizedRequest(req, model);
      return { kind: "submit", model: model, action: "text_to_video", requestBody: requestBody };
    },
    render: function (ctx, task) {
      const data = isObject(task && task.data) ? task.data : {};
      const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const output = {
        id: task.task_id,
        object: "video",
        model: trimmed((task.properties && task.properties.origin_model_name) || data.model),
        status: statusMap[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: task.created_at,
      };
      if (task.updated_at) output.completed_at = task.updated_at;
      if (task.status === "FAILURE") output.error = { message: trimmed(task.fail_reason || "task failed"), code: "task_failed" };
      return output;
    },
  },
};
