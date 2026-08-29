const MODEL_MAP = {
  "doubao-seedance-2.0": "dreamina-seedance-2-0-hc",
  "doubao-seedance-2.0-fast": "dreamina-seedance-2-0-fast-hc",
  "doubao-seedance-2.0-mini": "dreamina-seedance-2-0-mini-hc",
  "doubao-seedance-2.0-face": "dreamina-seedance-2-0-hc",
  "doubao-seedance-2.0-fast-face": "dreamina-seedance-2-0-fast-hc",
  "doubao-seedance-2.5": "dreamina-seedance-2-5-hc",
};

const MODELS = Object.keys(MODEL_MAP);
const REFERENCE_ERROR = "tokenmart v1 supports text-to-video only; reference image/video inputs are not supported";

export const meta = {
  apiVersion: 1,
  key: "tokenmart",
  name: "Tokenmart Seedance",
  icon: "Doubao.Color",
  description: {
    en: "Apimart Tokenmart Seedance text-to-video generation",
    zh: "Apimart Tokenmart Seedance 文生视频",
  },
  version: "1.0.0",
  author: { name: "QuantumNous" },
  models: MODELS,
  fetchMode: "per_task",
  usageSchema: {
    tokens: {
      type: "number",
      unit: "token",
      description: { en: "Estimated or upstream Seedance video tokens.", zh: "预估或上游返回的 Seedance 视频 token。" },
    },
    resolution: {
      enum: ["480p", "720p", "1080p", "4k"],
      description: { en: "Output video resolution tier.", zh: "输出视频分辨率档位。" },
    },
    video_input: {
      enum: ["none"],
      description: { en: "Tokenmart v1 text-to-video has no reference video input.", zh: "Tokenmart v1 文生视频不包含参考视频输入。" },
    },
  },
  usageExamples: [
    { label: "720p · 5s", facts: { tokens: 108000, resolution: "720p", video_input: "none" } },
    { label: "1080p · 5s", facts: { tokens: 243000, resolution: "1080p", video_input: "none" } },
  ],
  routes: [
    { method: "POST", path: "/v1/video/generate", type: "submit", decode: "createTask", render: "taskCreated", models: MODELS },
    { method: "GET", path: "/v1/video/tasks/:task_id", type: "query", render: "taskStatus" },
  ],
  protocols: ["openai_video"],
  auth: "api_key",
};

function trimmed(value) {
  return String(value === undefined || value === null ? "" : value).trim();
}

function isObject(value) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
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
    if (typeof item.text === "string" && trimmed(item.text)) texts.push(item.text);
    if (item.image_url !== undefined || item.video_url !== undefined || item.input_reference !== undefined || item.url !== undefined) throw new Error(REFERENCE_ERROR);
  }
  return texts.join("\n");
}

function textFromInput(input) {
  if (!Array.isArray(input)) return typeof input === "string" ? input : "";
  const texts = [];
  for (const item of input) {
    if (typeof item === "string") {
      if (trimmed(item)) texts.push(item);
      continue;
    }
    if (!isObject(item)) continue;
    if (item.content === undefined) {
      const type = trimmed(item.type).toLowerCase();
      if (type === "text" || type === "input_text") {
        if (typeof item.text === "string" && trimmed(item.text)) texts.push(item.text);
      } else {
        throw new Error(REFERENCE_ERROR);
      }
      if (item.image_url !== undefined || item.video_url !== undefined || item.url !== undefined) throw new Error(REFERENCE_ERROR);
      continue;
    }
    if (Array.isArray(item.content)) texts.push(textFromContent(item.content));
    else if (typeof item.content === "string" && trimmed(item.content)) texts.push(item.content);
    else if (item.content !== undefined) throw new Error(REFERENCE_ERROR);
  }
  return texts.filter(function (text) { return trimmed(text); }).join("\n");
}

function rejectRequestReferences(req) {
  if (!isObject(req)) return;
  for (const key of ["image", "images", "input_image", "input_images", "input_reference", "video", "videos", "video_url", "input_video", "reference", "references"])
    rejectReference(req[key]);
  if (req.content !== undefined) textFromContent(req.content);
  if (Array.isArray(req.input)) textFromInput(req.input);
}

function requestPrompt(req) {
  const texts = [];
  if (typeof req.prompt === "string" && trimmed(req.prompt)) texts.push(req.prompt);
  if (typeof req.text === "string" && trimmed(req.text)) texts.push(req.text);
  if (typeof req.content === "string" && trimmed(req.content)) texts.push(req.content);
  if (Array.isArray(req.content)) texts.push(textFromContent(req.content));
  if (typeof req.input === "string" && trimmed(req.input)) texts.push(req.input);
  if (Array.isArray(req.input)) texts.push(textFromInput(req.input));
  return texts.filter(function (text) { return trimmed(text); }).join("\n");
}

function normalizeDuration(value) {
  const seconds = Number(value);
  if (!Number.isFinite(seconds) || seconds <= 0 || seconds > 3600) throw new Error("duration must be between 1 and 3600 seconds");
  return seconds;
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

function normalizedRequest(req, model) {
  if (!isObject(req)) throw new Error("request body must be an object");
  rejectRequestReferences(req);
  const prompt = requestPrompt(req);
  if (!trimmed(prompt)) throw new Error("text prompt is required");
  const output = { model: model, prompt: prompt };
  const durationValue = req.duration === undefined ? req.seconds : req.duration;
  output.duration = durationValue === undefined ? 5 : normalizeDuration(durationValue);
  const resolutionValue = req.resolution === undefined ? req.size : req.resolution;
  output.resolution = normalizeResolution(resolutionValue);
  for (const key of ["ratio", "size", "generate_audio", "audio"]) {
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
    const data = isObject(task.data) ? task.data : {};
    const taskValue = isObject(data.task) ? Object.assign({}, data.task, { id: task.task_id }) : { id: task.task_id };
    return Object.assign({}, data, { task: taskValue });
  },
  taskStatus: function (ctx, task) {
    const data = isObject(task.data) ? task.data : {};
    const taskValue = isObject(data.task) ? Object.assign({}, data.task, { id: task.task_id }) : Object.assign({}, data, { id: task.task_id });
    return isObject(data.task) ? Object.assign({}, data, { task: taskValue }) : taskValue;
  },
  error: function (ctx, error) {
    return { error: { code: error.code, message: error.message } };
  },
};

export function buildSubmitRequest(ctx) {
  const req = isObject(ctx.requestBody) ? ctx.requestBody : {};
  const request = normalizedRequest(req, upstreamModel(ctx.upstreamModel || ctx.model || req.model));
  const body = {
    model: request.model,
    content: [{ type: "text", text: request.prompt }],
    duration: request.duration,
    resolution: request.resolution,
    watermark: false,
  };
  for (const key of ["ratio", "size", "generate_audio", "audio"]) {
    if (request[key] !== undefined) body[key] = request[key];
  }
  return {
    url: ctx.baseUrl + "/v1/video/generate",
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: body,
    action: "text_to_video",
    rewriteModel: body.model,
  };
}

function responseTask(body) {
  if (isObject(body) && isObject(body.task)) return body.task;
  return isObject(body) ? body : {};
}

function taskIdentifier(body) {
  const task = responseTask(body);
  return trimmed(task.id || task.task_id || (isObject(body) && (body.id || body.task_id)));
}

export function parseSubmitResponse(ctx, response) {
  const id = taskIdentifier(response && response.body);
  if (!id) throw new Error("task id is empty");
  return { taskId: id, taskData: response.body };
}

function resolutionMaxPixels(resolution) {
  if (resolution === "480p") return [854, 480];
  if (resolution === "1080p") return [1920, 1080];
  if (resolution === "4k") return [3840, 2160];
  return [1280, 720];
}

function estimateTokens(duration, resolution) {
  const dimensions = resolutionMaxPixels(resolution);
  return (duration * dimensions[0] * dimensions[1] * 24) / 1024;
}

export function extractUsage(ctx) {
  if (ctx && ctx.usagePurpose === "billing_ratios") return null;
  const req = isObject(ctx && ctx.requestBody) ? ctx.requestBody : {};
  let duration = req.duration === undefined ? req.seconds : req.duration;
  duration = duration === undefined ? 5 : Number(duration);
  if (!Number.isFinite(duration) || duration <= 0) duration = 5;
  duration = Math.min(duration, 3600);
  const resolution = normalizeResolution(req.resolution === undefined ? req.size : req.resolution);
  return { tokens: estimateTokens(duration, resolution), resolution: resolution, video_input: "none" };
}

export function extractUsageOnComplete(task, result, body) {
  const value = responseTask(body);
  const usage = isObject(value.usage) ? value.usage : isObject(body && body.usage) ? body.usage : {};
  const tokens = Number(usage.tokens || usage.total_tokens || usage.totalTokens || value.tokens);
  const facts = {};
  if (Number.isFinite(tokens) && tokens > 0) facts.tokens = tokens;
  const resolution = trimmed(value.resolution || (body && body.resolution)).toLowerCase();
  if (["480p", "720p", "1080p", "4k"].includes(resolution)) facts.resolution = resolution;
  if (Object.keys(facts).length === 0) return null;
  return facts;
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl + "/v1/video/tasks/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  const task = responseTask(body);
  const status = trimmed(task.status || (isObject(body) && body.status)).toLowerCase();
  const outputs = Array.isArray(task.outputs) ? task.outputs : Array.isArray(body && body.outputs) ? body.outputs : [];
  let url = "";
  for (const output of outputs) {
    const candidate = typeof output === "string" ? output : isObject(output) ? output.url || output.video_url : "";
    if (trimmed(candidate)) {
      url = trimmed(candidate);
      break;
    }
  }
  if (status === "pending" || status === "queued") return { status: "QUEUED", progress: "10%" };
  if (status === "processing" || status === "running") return { status: "IN_PROGRESS", progress: "50%" };
  if (status === "completed" || status === "succeeded") {
    const result = { status: "SUCCESS", progress: "100%" };
    if (url) result.url = url;
    const usage = isObject(task.usage) ? task.usage : isObject(body && body.usage) ? body.usage : {};
    const tokens = Number(usage.tokens || usage.total_tokens || usage.totalTokens);
    if (Number.isFinite(tokens) && tokens > 0) result.totalTokens = tokens;
    return result;
  }
  if (status === "failed" || status === "error") {
    const error = isObject(task.error) ? task.error : isObject(body && body.error) ? body.error : {};
    return { status: "FAILURE", progress: "100%", reason: trimmed(error.message || task.reason || task.fail_reason || status) };
  }
  return { status: "UNKNOWN", progress: "0%", reason: status ? "unknown task status: " + status : "task status is missing" };
}

function artifactURL(data) {
  const task = responseTask(data);
  const outputs = Array.isArray(task.outputs) ? task.outputs : [];
  for (const output of outputs) {
    const url = typeof output === "string" ? output : isObject(output) ? output.url || output.video_url : "";
    if (trimmed(url)) return trimmed(url);
  }
  return "";
}

export function listArtifacts(task) {
  return task && task.status === "SUCCESS" && artifactURL(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (!ctx || ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = artifactURL(ctx.data);
  if (!url) throw new Error("artifact_not_found");
  return { url: url, method: ctx.clientRequest.method, credentialless: true };
}


export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (!ctx.body || (ctx.body.kind !== "json" && ctx.body.kind !== "multipart")) throw new Error("JSON or multipart body required");
      if (ctx.body.kind === "json") {
        if (!isObject(ctx.body.value)) throw new Error("JSON object required");
        const body = Object.assign({}, ctx.body.value);
        const model = trimmed(ctx.model || body.model);
        if (!model) throw new Error("model is required");
        rejectRequestReferences(body);
        const requestBody = normalizedRequest(body, model);
        return { kind: "submit", model: model, action: "text_to_video", requestBody: requestBody };
      }
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
        rejectRequestReferences(metadata);
        Object.assign(request, metadata);
      }
      const model = trimmed(ctx.model || request.model);
      if (!model) throw new Error("model is required");
      if (request.duration !== undefined) request.duration = Number(request.duration);
      if (request.seconds !== undefined && request.duration === undefined) request.duration = Number(request.seconds);
      const requestBody = normalizedRequest(request, model);
      return { kind: "submit", model: model, action: "text_to_video", requestBody: requestBody };
    },
    render: function (ctx, task) {
      const data = isObject(task && task.data) ? task.data : {};
      const value = responseTask(data);
      const statusMap = { NOT_START: "queued", SUBMITTED: "queued", QUEUED: "queued", IN_PROGRESS: "in_progress", SUCCESS: "completed", FAILURE: "failed" };
      const output = {
        id: task.task_id,
        object: "video",
        model: trimmed((task.properties && task.properties.origin_model_name) || value.model),
        status: statusMap[task.status] || "unknown",
        progress: Number(String(task.progress || "0").replace("%", "")),
        created_at: task.created_at,
      };
      if (task.updated_at) output.completed_at = task.updated_at;
      if (task.status === "FAILURE") {
        const error = isObject(value.error) ? value.error : {};
        output.error = { message: trimmed(error.message || task.fail_reason || "task failed"), code: trimmed(error.code || "task_failed") };
      }
      return output;
    },
  },
};
