/** Self-contained: the same function is embedded in the DSM window. True only
 * when DSM refused a request because this method version is unavailable, the
 * one refusal that may be retried with an older version. DSM 7.1.1
 * returns 104 for a version above maxVersion and 103 for a method absent at an
 * existing version. Accepts the numeric code and the window's dsm_ form. */
export function versionUnsupported(code: unknown): boolean {
  return ["103", "104", "dsm_103", "dsm_104"].indexOf(String(code)) >= 0;
}
