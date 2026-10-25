import type { ProviderTemplate } from "@/lib/api";

export type ConfigFieldMap = Record<string, string>;

export const parseConfigJson = (raw?: string | null): ConfigFieldMap | null => {
  if (!raw) return null;

  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      return null;
    }

    const entries: [string, string][] = [];
    for (const [key, value] of Object.entries(parsed)) {
      if (["string", "number", "boolean"].includes(typeof value)) {
        entries.push([key, String(value)]);
        continue;
      }
      return null;
    }

    return Object.fromEntries(entries);
  } catch {
    return null;
  }
};

export const stringifyConfigFields = (fields: ConfigFieldMap) =>
  JSON.stringify(fields, null, 2);

export const mergeTemplateWithConfig = (
  templateFields: ConfigFieldMap,
  existingConfig: ConfigFieldMap | null,
  preserveUnknownKeys: boolean
): ConfigFieldMap => {
  if (!existingConfig) {
    return templateFields;
  }

  if (preserveUnknownKeys) {
    return { ...templateFields, ...existingConfig };
  }

  const merged: ConfigFieldMap = {};
  for (const [key, value] of Object.entries(templateFields)) {
    merged[key] = Object.prototype.hasOwnProperty.call(existingConfig, key)
      ? existingConfig[key]
      : value;
  }
  return merged;
};

export const getTemplateInitialConfig = (template?: ProviderTemplate): string => {
  if (!template) {
    return "";
  }

  const parsed = parseConfigJson(template.template);
  return parsed ? stringifyConfigFields(parsed) : template.template;
};

export const getConfigBaseUrl = (config: string): string => {
  const parsed = parseConfigJson(config);
  return parsed?.base_url ?? "未设置";
};

/**
 * 提供商专属地址的名称标识规则，与服务端 isSafeProviderSegment 保持一致：
 * 首字符为字母或数字，其余为字母、数字、-、_、.，长度 1-64。
 * 不满足该规则的名称无法生成专属地址。
 */
export const isSafeProviderName = (name: string): boolean => {
  if (name.length === 0 || name.length > 64) return false;
  for (let i = 0; i < name.length; i++) {
    const c = name.charCodeAt(i);
    const isAlnum =
      (c >= 97 && c <= 122) || (c >= 65 && c <= 90) || (c >= 48 && c <= 57);
    if (!isAlnum && (i === 0 || !(c === 45 || c === 95 || c === 46))) {
      return false;
    }
  }
  return true;
};

/** 提供商专属 API 基础地址：/p/{name}/v1。 */
export const dedicatedBaseUrl = (name: string): string =>
  `${window.location.origin}/p/${name}/v1`;
