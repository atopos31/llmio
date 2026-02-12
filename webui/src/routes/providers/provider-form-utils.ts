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
