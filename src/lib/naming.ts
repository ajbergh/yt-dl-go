export const namingTokenNames = [
  "{channel}", "{title}", "{resolution}", "{category}", "{id}", "{upload_date}",
  "{playlist}", "{index}", "{ext}", "{fps}", "{codec}",
] as const;

export type NamingValues = Record<(typeof namingTokenNames)[number], string>;

export function expandNamingPattern(pattern: string, values: NamingValues): string {
  let output = "";
  for (let index = 0; index < pattern.length;) {
    if (pattern[index] !== "{") {
      output += pattern[index++];
      continue;
    }
    const end = pattern.indexOf("}", index);
    if (end < 0) {
      output += pattern.slice(index);
      break;
    }
    const token = pattern.slice(index, end + 1) as keyof NamingValues;
    output += Object.hasOwn(values, token) ? values[token] : token;
    index = end + 1;
  }
  return output;
}

export function sanitizeFilenameComponent(value: string): string {
  let output = "";
  let lastDash = false;
  for (const character of value.trim()) {
    if (/\p{Cc}/u.test(character) || /[<>:"/\\|?*]/u.test(character)) {
      if (!lastDash) output += "-";
      lastDash = true;
      continue;
    }
    output += character;
    lastDash = character === "-";
  }
  output = output.replace(/^[ .]+|[ .]+$/gu, "");
  const device = output.split(".", 1)[0].toUpperCase();
  if (["CON", "PRN", "AUX", "NUL"].includes(device) || /^(COM|LPT)[1-9]$/u.test(device)) output = `_${output}`;
  return Array.from(output).slice(0, 170).join("") || "Untitled";
}

export function previewFilename(pattern: string, values: NamingValues, extension: string): string {
  const base = sanitizeFilenameComponent(expandNamingPattern(pattern, values));
  const suffix = extension.startsWith(".") ? extension : `.${extension}`;
  const finalExtension = base.toLowerCase().endsWith(suffix.toLowerCase()) ? "" : suffix;
  return `${base}${finalExtension}`;
}
