import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const api = require("./dashboard.js");
export const clip = api.clip;
export const formatUsage = api.formatUsage;
export const summarizeEvents = api.summarizeEvents;
export const formatComparison = api.formatComparison;
export const mergeEvents = api.mergeEvents;
