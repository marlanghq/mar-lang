#!/usr/bin/env node

import fs from "node:fs";
import path from "node:path";

const TYPE_MAP = {
  Int: "INTEGER",
  String: "TEXT",
  Bool: "INTEGER",
  Float: "REAL",
};

function fail(message) {
  throw new Error(message);
}

function isName(value) {
  return /^[A-Za-z][A-Za-z0-9_]*$/.test(value);
}

function toSnake(value) {
  return value
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/[-\s]+/g, "_")
    .toLowerCase();
}

function pluralize(value) {
  if (value.endsWith("y") && !/[aeiou]y$/.test(value)) {
    return `${value.slice(0, -1)}ies`;
  }
  if (value.endsWith("s")) {
    return `${value}es`;
  }
  return `${value}s`;
}

function compileRuleExpression(expression, fieldNames) {
  const source = expression.trim();
  if (!source) {
    fail("rule expression is empty");
  }

  const tokens = [];
  let i = 0;
  while (i < source.length) {
    const ch = source[i];

    if (/\s/.test(ch)) {
      i += 1;
      continue;
    }

    const two = source.slice(i, i + 2);
    if (["<=", ">=", "==", "!="].includes(two)) {
      tokens.push({ type: "op", value: two });
      i += 2;
      continue;
    }

    if (["(", ")", ",", "+", "-", "*", "/", "<", ">"].includes(ch)) {
      tokens.push({ type: "op", value: ch });
      i += 1;
      continue;
    }

    if (ch === '"') {
      let j = i + 1;
      let closed = false;
      while (j < source.length) {
        if (source[j] === "\\") {
          j += 2;
          continue;
        }
        if (source[j] === '"') {
          closed = true;
          break;
        }
        j += 1;
      }
      if (!closed) {
        fail("unterminated string literal in rule expression");
      }
      const rawString = source.slice(i, j + 1);
      let value;
      try {
        value = JSON.parse(rawString);
      } catch {
        fail("invalid string literal in rule expression");
      }
      tokens.push({ type: "string", value });
      i = j + 1;
      continue;
    }

    const numberMatch = source.slice(i).match(/^[0-9]+(?:\.[0-9]+)?/);
    if (numberMatch) {
      tokens.push({ type: "number", value: numberMatch[0] });
      i += numberMatch[0].length;
      continue;
    }

    const wordMatch = source.slice(i).match(/^[A-Za-z_][A-Za-z0-9_]*/);
    if (wordMatch) {
      tokens.push({ type: "word", value: wordMatch[0] });
      i += wordMatch[0].length;
      continue;
    }

    fail(`invalid character "${ch}" in rule expression`);
  }

  const fields = new Set(fieldNames);
  let idx = 0;

  const peek = () => tokens[idx] ?? null;
  const consume = () => tokens[idx++] ?? null;
  const peekOp = (value) => {
    const token = peek();
    return token && token.type === "op" && token.value === value;
  };
  const peekWord = (value) => {
    const token = peek();
    return token && token.type === "word" && token.value === value;
  };
  const consumeOp = (value) => {
    if (!peekOp(value)) {
      fail(`expected "${value}"`);
    }
    consume();
  };

  function parseExpression() {
    return parseOr();
  }

  function parseOr() {
    let left = parseAnd();
    while (peekWord("or")) {
      consume();
      const right = parseAnd();
      left = `(${left} || ${right})`;
    }
    return left;
  }

  function parseAnd() {
    let left = parseEquality();
    while (peekWord("and")) {
      consume();
      const right = parseEquality();
      left = `(${left} && ${right})`;
    }
    return left;
  }

  function parseEquality() {
    let left = parseComparison();
    while (peekOp("==") || peekOp("!=")) {
      const operator = consume().value;
      const right = parseComparison();
      left = operator === "==" ? `(${left} === ${right})` : `(${left} !== ${right})`;
    }
    return left;
  }

  function parseComparison() {
    let left = parseTerm();
    while (peekOp(">") || peekOp(">=") || peekOp("<") || peekOp("<=")) {
      const operator = consume().value;
      const right = parseTerm();
      left = `(${left} ${operator} ${right})`;
    }
    return left;
  }

  function parseTerm() {
    let left = parseFactor();
    while (peekOp("+") || peekOp("-")) {
      const operator = consume().value;
      const right = parseFactor();
      left = `(${left} ${operator} ${right})`;
    }
    return left;
  }

  function parseFactor() {
    let left = parseUnary();
    while (peekOp("*") || peekOp("/")) {
      const operator = consume().value;
      const right = parseUnary();
      left = `(${left} ${operator} ${right})`;
    }
    return left;
  }

  function parseUnary() {
    if (peekWord("not")) {
      consume();
      const value = parseUnary();
      return `(!(${value}))`;
    }
    if (peekOp("-")) {
      consume();
      const value = parseUnary();
      return `(-(${value}))`;
    }
    return parsePrimary();
  }

  function parseCall(name) {
    consumeOp("(");
    const args = [];
    if (!peekOp(")")) {
      args.push(parseExpression());
      while (peekOp(",")) {
        consume();
        args.push(parseExpression());
      }
    }
    consumeOp(")");

    if (name === "contains") {
      if (args.length !== 2) fail("contains expects 2 arguments");
      return `belmContains(${args[0]}, ${args[1]})`;
    }
    if (name === "startsWith") {
      if (args.length !== 2) fail("startsWith expects 2 arguments");
      return `belmStartsWith(${args[0]}, ${args[1]})`;
    }
    if (name === "endsWith") {
      if (args.length !== 2) fail("endsWith expects 2 arguments");
      return `belmEndsWith(${args[0]}, ${args[1]})`;
    }
    if (name === "len") {
      if (args.length !== 1) fail("len expects 1 argument");
      return `belmLen(${args[0]})`;
    }
    if (name === "matches") {
      if (args.length !== 2) fail("matches expects 2 arguments");
      return `belmMatches(${args[0]}, ${args[1]})`;
    }
    fail(`unknown function "${name}" in rule expression`);
  }

  function parsePrimary() {
    const token = peek();
    if (!token) {
      fail("unexpected end of rule expression");
    }

    if (token.type === "number") {
      consume();
      return token.value;
    }
    if (token.type === "string") {
      consume();
      return JSON.stringify(token.value);
    }
    if (token.type === "word") {
      consume();
      if (token.value === "true") return "true";
      if (token.value === "false") return "false";
      if (peekOp("(")) {
        return parseCall(token.value);
      }
      if (!fields.has(token.value)) {
        fail(`unknown identifier "${token.value}" in rule expression`);
      }
      return `ctx[${JSON.stringify(token.value)}]`;
    }
    if (peekOp("(")) {
      consume();
      const inner = parseExpression();
      consumeOp(")");
      return `(${inner})`;
    }

    fail(`unexpected token "${token.value}" in rule expression`);
  }

  const compiled = parseExpression();
  if (idx !== tokens.length) {
    const token = tokens[idx];
    fail(`unexpected token "${token.value}" in rule expression`);
  }
  return compiled;
}

function parseBelm(source) {
  const lines = source.replace(/\r/g, "").split("\n");
  const app = {
    appName: null,
    port: 3000,
    database: "./app.db",
    entities: [],
  };

  let i = 0;
  const nextLine = () => {
    if (i >= lines.length) {
      return null;
    }
    return { text: lines[i], number: i + 1 };
  };
  const advance = () => {
    i += 1;
  };

  while (true) {
    const current = nextLine();
    if (!current) {
      break;
    }
    const trimmed = current.text.trim();
    if (!trimmed || trimmed.startsWith("--") || trimmed.startsWith("#")) {
      advance();
      continue;
    }

    const appMatch = trimmed.match(/^app\s+([A-Za-z][A-Za-z0-9_]*)$/);
    if (appMatch) {
      app.appName = appMatch[1];
      advance();
      continue;
    }

    const portMatch = trimmed.match(/^port\s+([0-9]{1,5})$/);
    if (portMatch) {
      const port = Number(portMatch[1]);
      if (port < 1 || port > 65535) {
        fail(`Line ${current.number}: invalid port ${port}`);
      }
      app.port = port;
      advance();
      continue;
    }

    const dbMatch = trimmed.match(/^database\s+"([^"]+)"$/);
    if (dbMatch) {
      app.database = dbMatch[1];
      advance();
      continue;
    }

    const entityMatch = trimmed.match(/^entity\s+([A-Za-z][A-Za-z0-9_]*)\s*\{$/);
    if (entityMatch) {
      const name = entityMatch[1];
      const fields = [];
      const rules = [];
      advance();
      let closed = false;

      while (true) {
        const entityLine = nextLine();
        if (!entityLine) {
          fail(`Entity ${name} is missing closing "}"`);
        }

        const entityTrimmed = entityLine.text.trim();
        if (!entityTrimmed || entityTrimmed.startsWith("--") || entityTrimmed.startsWith("#")) {
          advance();
          continue;
        }

        if (entityTrimmed === "}") {
          closed = true;
          advance();
          break;
        }

        const ruleMatch = entityTrimmed.match(/^rule\s+"([^"]+)"\s+when\s+(.+)$/);
        if (ruleMatch) {
          const message = ruleMatch[1].trim();
          const expression = ruleMatch[2].trim();
          if (!message) {
            fail(`Line ${entityLine.number}: rule message cannot be empty`);
          }
          if (!expression) {
            fail(`Line ${entityLine.number}: rule expression cannot be empty`);
          }
          rules.push({
            message,
            expression,
            line: entityLine.number,
          });
          advance();
          continue;
        }

        const fieldMatch = entityTrimmed.match(
          /^([a-z][A-Za-z0-9_]*)\s*:\s*(Int|String|Bool|Float)(?:\s+(.*))?$/
        );
        if (!fieldMatch) {
          fail(`Line ${entityLine.number}: invalid field declaration "${entityTrimmed}"`);
        }

        const fieldName = fieldMatch[1];
        const fieldType = fieldMatch[2];
        const rawAttrs = fieldMatch[3] ? fieldMatch[3].trim() : "";
        const attrs = rawAttrs ? rawAttrs.split(/\s+/) : [];
        const allowed = new Set(["primary", "auto", "optional"]);
        for (const attr of attrs) {
          if (!allowed.has(attr)) {
            fail(`Line ${entityLine.number}: unknown field attribute "${attr}"`);
          }
        }
        fields.push({
          name: fieldName,
          type: fieldType,
          primary: attrs.includes("primary"),
          auto: attrs.includes("auto"),
          optional: attrs.includes("optional"),
        });
        advance();
      }

      if (!closed) {
        fail(`Entity ${name} is missing closing "}"`);
      }
      if (!fields.length) {
        fail(`Entity ${name} has no fields`);
      }
      const primaryFields = fields.filter((f) => f.primary);
      if (primaryFields.length > 1) {
        fail(`Entity ${name} has multiple primary fields`);
      }
      if (primaryFields.length === 0) {
        fields.unshift({
          name: "id",
          type: "Int",
          primary: true,
          auto: true,
          optional: false,
        });
      }

      const finalPrimary = fields.find((f) => f.primary);
      if (!finalPrimary) {
        fail(`Entity ${name} requires a primary field`);
      }
      if (!isName(name)) {
        fail(`Entity name "${name}" is invalid`);
      }
      for (const field of fields) {
        if (!isName(field.name)) {
          fail(`Field name "${field.name}" in ${name} is invalid`);
        }
      }
      const fieldNames = fields.map((field) => field.name);
      const compiledRules = rules.map((rule) => {
        try {
          return {
            message: rule.message,
            expression: rule.expression,
            js: compileRuleExpression(rule.expression, fieldNames),
          };
        } catch (error) {
          fail(`Line ${rule.line}: invalid rule expression "${rule.expression}" (${error.message})`);
        }
      });

      const tableBase = pluralize(toSnake(name));
      app.entities.push({
        name,
        table: tableBase,
        resource: `/${tableBase}`,
        primaryKey: finalPrimary.name,
        fields,
        rules: compiledRules,
      });
      continue;
    }

    fail(`Line ${current.number}: unknown statement "${trimmed}"`);
  }

  if (!app.appName) {
    fail(`Missing app declaration. Example: app TodoApi`);
  }
  if (!app.entities.length) {
    fail(`At least one entity is required`);
  }

  return app;
}

function generateServer(app) {
  const appJson = JSON.stringify(app, null, 2);
  return `#!/usr/bin/env node
/**
 * Generated by belmc (Belm compiler)
 * Source language: Belm (Elm-inspired, backend-oriented)
 */

import http from "node:http";
import { URL } from "node:url";
import { DatabaseSync } from "node:sqlite";

const APP = ${appJson};
const db = new DatabaseSync(APP.database);
const ENTITY_RULES = new Map();

function belmError(statusCode, message, details) {
  const error = new Error(message);
  error.statusCode = statusCode;
  if (details) {
    error.details = details;
  }
  return error;
}

function belmContains(left, right) {
  if (left === null || left === undefined) return false;
  return String(left).includes(String(right));
}

function belmStartsWith(left, right) {
  if (left === null || left === undefined) return false;
  return String(left).startsWith(String(right));
}

function belmEndsWith(left, right) {
  if (left === null || left === undefined) return false;
  return String(left).endsWith(String(right));
}

function belmLen(value) {
  if (value === null || value === undefined) return 0;
  if (typeof value === "string" || Array.isArray(value)) return value.length;
  return 0;
}

function belmMatches(value, pattern) {
  const subject = value === null || value === undefined ? "" : String(value);
  try {
    return new RegExp(String(pattern)).test(subject);
  } catch {
    return false;
  }
}

function quoteIdentifier(name) {
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) {
    throw new Error(\`Unsafe SQL identifier: \${name}\`);
  }
  return '"' + name + '"';
}

function sqlType(fieldType) {
  switch (fieldType) {
    case "Int":
      return "INTEGER";
    case "String":
      return "TEXT";
    case "Bool":
      return "INTEGER";
    case "Float":
      return "REAL";
    default:
      throw new Error(\`Unsupported type: \${fieldType}\`);
  }
}

function normalizeForDb(field, value) {
  if (value === null || value === undefined) {
    return null;
  }
  switch (field.type) {
    case "Int":
      if (!Number.isInteger(value)) throw new Error(\`Field \${field.name} must be Int\`);
      return value;
    case "Float":
      if (typeof value !== "number" || Number.isNaN(value)) throw new Error(\`Field \${field.name} must be Float\`);
      return value;
    case "String":
      if (typeof value !== "string") throw new Error(\`Field \${field.name} must be String\`);
      return value;
    case "Bool":
      if (typeof value !== "boolean") throw new Error(\`Field \${field.name} must be Bool\`);
      return value ? 1 : 0;
    default:
      throw new Error(\`Unknown type for \${field.name}\`);
  }
}

function decodeFromDb(field, value) {
  if (value === null || value === undefined) return null;
  if (field.type === "Bool") {
    return value === 1;
  }
  return value;
}

function decodeRow(entity, row) {
  if (!row) return row;
  const output = {};
  for (const field of entity.fields) {
    output[field.name] = decodeFromDb(field, row[field.name]);
  }
  return output;
}

function compileRules() {
  for (const entity of APP.entities) {
    const compiled = (entity.rules || []).map((rule) => {
      try {
        const fn = new Function(
          "ctx",
          "belmContains",
          "belmStartsWith",
          "belmEndsWith",
          "belmLen",
          "belmMatches",
          "return (" + rule.js + ");"
        );
        return {
          message: rule.message,
          expression: rule.expression,
          fn,
        };
      } catch (error) {
        throw new Error("Failed to compile rule for " + entity.name + ": " + error.message);
      }
    });
    ENTITY_RULES.set(entity.name, compiled);
  }
}

function validateEntityRules(entity, context) {
  const rules = ENTITY_RULES.get(entity.name) || [];
  for (const rule of rules) {
    let passed = false;
    try {
      passed = Boolean(
        rule.fn(context, belmContains, belmStartsWith, belmEndsWith, belmLen, belmMatches)
      );
    } catch {
      passed = false;
    }
    if (!passed) {
      throw belmError(422, rule.message, {
        entity: entity.name,
        rule: rule.expression,
      });
    }
  }
}

function parsePrimaryValue(entity, raw) {
  const pk = entity.fields.find((f) => f.name === entity.primaryKey);
  if (!pk) throw new Error(\`Primary key not found for \${entity.name}\`);
  if (pk.type === "Int") {
    const numberValue = Number(raw);
    if (!Number.isInteger(numberValue)) return { ok: false, value: null };
    return { ok: true, value: numberValue };
  }
  if (pk.type === "Float") {
    const numberValue = Number(raw);
    if (Number.isNaN(numberValue)) return { ok: false, value: null };
    return { ok: true, value: numberValue };
  }
  if (pk.type === "Bool") {
    if (raw === "true") return { ok: true, value: 1 };
    if (raw === "false") return { ok: true, value: 0 };
    return { ok: false, value: null };
  }
  return { ok: true, value: String(raw) };
}

function createSchema() {
  for (const entity of APP.entities) {
    const columnDefs = entity.fields.map((field) => {
      const parts = [quoteIdentifier(field.name), sqlType(field.type)];
      if (field.primary) parts.push("PRIMARY KEY");
      if (field.auto) parts.push("AUTOINCREMENT");
      if (!field.optional && !field.primary) parts.push("NOT NULL");
      return parts.join(" ");
    });
    const sql = \`CREATE TABLE IF NOT EXISTS \${quoteIdentifier(entity.table)} (\${columnDefs.join(", ")});\`;
    db.exec(sql);
  }
}

function json(res, statusCode, payload) {
  const body = JSON.stringify(payload, null, 2);
  res.writeHead(statusCode, {
    "content-type": "application/json; charset=utf-8",
  });
  res.end(body);
}

async function readJsonBody(req) {
  const chunks = [];
  for await (const chunk of req) {
    chunks.push(chunk);
  }
  if (chunks.length === 0) return {};
  const raw = Buffer.concat(chunks).toString("utf8").trim();
  if (!raw) return {};
  try {
    return JSON.parse(raw);
  } catch {
    throw new Error("Invalid JSON body");
  }
}

function handleList(entity, res) {
  const sql = \`SELECT * FROM \${quoteIdentifier(entity.table)} ORDER BY \${quoteIdentifier(entity.primaryKey)} DESC\`;
  const rows = db.prepare(sql).all();
  json(res, 200, rows.map((row) => decodeRow(entity, row)));
}

function handleGet(entity, idValue, res) {
  const sql = \`SELECT * FROM \${quoteIdentifier(entity.table)} WHERE \${quoteIdentifier(entity.primaryKey)} = ?\`;
  const row = db.prepare(sql).get(idValue);
  if (!row) {
    json(res, 404, { error: \`\${entity.name} not found\` });
    return;
  }
  json(res, 200, decodeRow(entity, row));
}

function handleDelete(entity, idValue, res) {
  const sql = \`DELETE FROM \${quoteIdentifier(entity.table)} WHERE \${quoteIdentifier(entity.primaryKey)} = ?\`;
  const result = db.prepare(sql).run(idValue);
  if (!result.changes) {
    json(res, 404, { error: \`\${entity.name} not found\` });
    return;
  }
  json(res, 200, { deleted: true, id: idValue });
}

function fieldMap(entity) {
  const map = new Map();
  for (const field of entity.fields) {
    map.set(field.name, field);
  }
  return map;
}

function assertPayloadObject(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    throw new Error("JSON body must be an object");
  }
}

function assertNoUnknownFields(entity, payload, mode) {
  const map = fieldMap(entity);
  for (const key of Object.keys(payload)) {
    const field = map.get(key);
    if (!field) {
      throw new Error(\`Unknown field \${key}\`);
    }
    if (mode === "create" && field.primary && field.auto) {
      throw new Error(\`Field \${key} is auto-generated and cannot be provided\`);
    }
    if (mode === "update" && field.primary) {
      throw new Error(\`Field \${key} cannot be updated\`);
    }
  }
}

function buildInsert(entity, payload) {
  assertPayloadObject(payload);
  assertNoUnknownFields(entity, payload, "create");

  const values = [];
  const columns = [];
  const context = {};

  for (const field of entity.fields) {
    const hasValue = Object.prototype.hasOwnProperty.call(payload, field.name);
    if (field.primary && field.auto) {
      context[field.name] = null;
      continue;
    }
    if (!hasValue) {
      if (!field.optional) {
        throw new Error(\`Missing required field \${field.name}\`);
      }
      context[field.name] = null;
      continue;
    }
    const value = normalizeForDb(field, payload[field.name]);
    context[field.name] = decodeFromDb(field, value);
    columns.push(field.name);
    values.push(value);
  }
  return { columns, values, context };
}

function buildUpdate(entity, payload, currentContext) {
  assertPayloadObject(payload);
  assertNoUnknownFields(entity, payload, "update");

  const updatable = entity.fields.filter((f) => !(f.primary && f.auto) && !f.primary);
  const columns = [];
  const values = [];
  const context = { ...currentContext };
  for (const field of updatable) {
    if (!Object.prototype.hasOwnProperty.call(payload, field.name)) {
      continue;
    }
    columns.push(field.name);
    const normalized = normalizeForDb(field, payload[field.name]);
    values.push(normalized);
    context[field.name] = decodeFromDb(field, normalized);
  }
  if (!columns.length) {
    throw new Error("No updatable fields provided");
  }
  return { columns, values, context };
}

function fetchById(entity, idValue) {
  const sql = \`SELECT * FROM \${quoteIdentifier(entity.table)} WHERE \${quoteIdentifier(entity.primaryKey)} = ?\`;
  return db.prepare(sql).get(idValue);
}

function handleCreate(entity, payload, res) {
  const built = buildInsert(entity, payload);
  validateEntityRules(entity, built.context);
  let idValue = payload[entity.primaryKey];
  if (built.columns.length === 0) {
    const result = db.prepare(\`INSERT INTO \${quoteIdentifier(entity.table)} DEFAULT VALUES\`).run();
    idValue = Number(result.lastInsertRowid);
  } else {
    const columnSql = built.columns.map(quoteIdentifier).join(", ");
    const placeholderSql = built.columns.map(() => "?").join(", ");
    const sql = \`INSERT INTO \${quoteIdentifier(entity.table)} (\${columnSql}) VALUES (\${placeholderSql})\`;
    const result = db.prepare(sql).run(...built.values);
    const pkField = entity.fields.find((f) => f.name === entity.primaryKey);
    if (pkField && pkField.auto) {
      idValue = Number(result.lastInsertRowid);
    }
  }
  const created = fetchById(entity, idValue);
  json(res, 201, decodeRow(entity, created));
}

function handleUpdate(entity, idValue, payload, res) {
  const existing = fetchById(entity, idValue);
  if (!existing) {
    json(res, 404, { error: \`\${entity.name} not found\` });
    return;
  }

  const built = buildUpdate(entity, payload, decodeRow(entity, existing));
  validateEntityRules(entity, built.context);
  const setSql = built.columns.map((name) => \`\${quoteIdentifier(name)} = ?\`).join(", ");
  const sql = \`UPDATE \${quoteIdentifier(entity.table)} SET \${setSql} WHERE \${quoteIdentifier(entity.primaryKey)} = ?\`;
  const result = db.prepare(sql).run(...built.values, idValue);
  if (!result.changes) {
    json(res, 404, { error: \`\${entity.name} not found\` });
    return;
  }
  const updated = fetchById(entity, idValue);
  json(res, 200, decodeRow(entity, updated));
}

compileRules();
createSchema();

const server = http.createServer(async (req, res) => {
  try {
    const method = req.method || "GET";
    const url = new URL(req.url || "/", "http://localhost");
    const pathname = url.pathname.replace(/\\/$/, "") || "/";

    if (method === "GET" && pathname === "/health") {
      json(res, 200, { ok: true, app: APP.appName });
      return;
    }

    for (const entity of APP.entities) {
      const base = entity.resource;
      if (method === "GET" && pathname === base) {
        handleList(entity, res);
        return;
      }
      if (method === "POST" && pathname === base) {
        const payload = await readJsonBody(req);
        handleCreate(entity, payload, res);
        return;
      }

      const prefix = base + "/";
      if (pathname.startsWith(prefix)) {
        const rawId = decodeURIComponent(pathname.slice(prefix.length));
        if (!rawId || rawId.includes("/")) {
          continue;
        }
        const parsed = parsePrimaryValue(entity, rawId);
        if (!parsed.ok) {
          json(res, 400, { error: \`Invalid \${entity.primaryKey}\` });
          return;
        }
        if (method === "GET") {
          handleGet(entity, parsed.value, res);
          return;
        }
        if (method === "PUT" || method === "PATCH") {
          const payload = await readJsonBody(req);
          handleUpdate(entity, parsed.value, payload, res);
          return;
        }
        if (method === "DELETE") {
          handleDelete(entity, parsed.value, res);
          return;
        }
      }
    }

    json(res, 404, { error: "Route not found" });
  } catch (error) {
    const statusCode = Number.isInteger(error.statusCode) ? error.statusCode : 400;
    const payload = { error: error.message };
    if (error.details) {
      payload.details = error.details;
    }
    json(res, statusCode, payload);
  }
});

server.listen(APP.port, () => {
  console.log(\`Belm app "\${APP.appName}" running on http://localhost:\${APP.port}\`);
  console.log(\`SQLite database: \${APP.database}\`);
  for (const entity of APP.entities) {
    console.log(\`CRUD endpoints: \${entity.resource}\`);
  }
});
`;
}

function main() {
  const [, , inputPath, outputPath] = process.argv;
  if (!inputPath || !outputPath) {
    console.error("Usage: node src/belmc.mjs <input.belm> <output.mjs>");
    process.exit(1);
  }

  const absoluteInput = path.resolve(process.cwd(), inputPath);
  const absoluteOutput = path.resolve(process.cwd(), outputPath);
  const source = fs.readFileSync(absoluteInput, "utf8");
  const app = parseBelm(source);
  const generated = generateServer(app);

  fs.mkdirSync(path.dirname(absoluteOutput), { recursive: true });
  fs.writeFileSync(absoluteOutput, generated, "utf8");
  fs.chmodSync(absoluteOutput, 0o755);
  console.log(`Compiled ${inputPath} -> ${outputPath}`);
}

main();
