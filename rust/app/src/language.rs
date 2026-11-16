//! `app.status` language: the caller's DSM display language, as the Go SDK's
//! `api.AppStatusHandler` decides it with no custom language.
//!
//! The entry page reads the DSM display language on DSM's own origin and sends
//! it as `app.status {"dsmLanguage": "cht"}`. The hint wins and is remembered
//! for the user in this process; without one the user's last hint applies,
//! else the browser's first `Accept-Language` tag ([`Context::locale`]). "def"
//! (DSM follows the browser), an empty hint and a DSM language without an SDK
//! locale follow the browser. The App never reads DSM itself. The DSM code
//! table is generated from `spec/language/dsm-languages.json`, as Go's and
//! TypeScript's are.

use crate::dsm_languages::DSM_LOCALES;
use crate::{Context, Error, Params};
use std::collections::HashMap;
use std::sync::{LazyLock, Mutex};

/// Users the per-process cache keeps; the least recently stored goes first.
const APP_LANGUAGE_USERS: usize = 256;

/// Entry-page display metadata, the Go SDK's `api.AppStatus`. The runtime
/// still checks the real session and the route policy on every request.
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize)]
pub struct AppStatus {
    pub package: String,
    pub version: String,
    pub user: String,
    pub admin: bool,
    /// The DSM display-language code (cht, enu, …), or empty.
    pub lang: String,
    /// The UI locale (zh-TW, en-US, …), or empty to follow the browser.
    pub locale: String,
}

/// `app.status` with the SDK's language decision. Register it under an
/// authenticated policy: `app.method("app.status", |ctx, p| app_status(ctx, p, ID, VERSION))`.
/// An App with fields of its own builds them around [`app_language`].
pub fn app_status(
    ctx: &Context,
    params: &Params,
    package: &str,
    version: &str,
) -> Result<AppStatus, Error> {
    if ctx.user.is_empty() {
        return Err(Error::new("session_required", "sign in to DSM again"));
    }
    let (lang, locale) = app_language(ctx, params);
    Ok(AppStatus {
        package: package.into(),
        version: version.into(),
        user: ctx.user.clone(),
        admin: ctx.admin,
        lang,
        locale,
    })
}

/// The caller's `(lang, locale)` for `app.status`: the params' `dsmLanguage`
/// hint, else the user's last hint in this process, else the browser's
/// language. Both empty mean the entry page uses the browser's language.
pub fn app_language(ctx: &Context, params: &Params) -> (String, String) {
    let mut cache = LANGUAGES
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner());
    cache.decide(&ctx.user, params.get(), &ctx.locale)
}

static LANGUAGES: LazyLock<Mutex<LanguageCache>> =
    LazyLock::new(|| Mutex::new(LanguageCache::default()));

/// Each user's DSM display language as the entry page last reported it: a DSM
/// code with an SDK locale, or "" to follow the browser.
#[derive(Default)]
pub(crate) struct LanguageCache {
    users: HashMap<String, String>,
    order: Vec<String>,
}

impl LanguageCache {
    /// `(lang, locale)` for one request; `accept_language` is the request's
    /// Accept-Language (its first tag is used).
    pub(crate) fn decide(
        &mut self,
        user: &str,
        params: &str,
        accept_language: &str,
    ) -> (String, String) {
        let mut lang = self.preference(user, params);
        if lang.is_empty() {
            let first = accept_language.split(',').next().unwrap_or("");
            lang = first.split(';').next().unwrap_or("").trim().to_string();
        }
        normalize(&lang)
    }

    /// Stores and answers the params' hint, else the user's last known hint,
    /// else "" (the browser's language).
    fn preference(&mut self, user: &str, params: &str) -> String {
        if user.is_empty() {
            return String::new();
        }
        if let Some(hint) = language_hint(params) {
            self.store(user, &hint);
            return hint;
        }
        self.users.get(user).cloned().unwrap_or_default()
    }

    pub(crate) fn store(&mut self, user: &str, lang: &str) {
        if self.users.contains_key(user) {
            self.order.retain(|name| name != user);
        } else if self.users.len() >= APP_LANGUAGE_USERS {
            let oldest = self.order.remove(0);
            self.users.remove(&oldest);
        }
        self.users.insert(user.into(), lang.into());
        self.order.push(user.into());
    }

    #[cfg(test)]
    pub(crate) fn get(&self, user: &str) -> Option<&str> {
        self.users.get(user).map(String::as_str)
    }

    #[cfg(test)]
    pub(crate) fn len(&self) -> usize {
        self.users.len()
    }
}

/// The params' `dsmLanguage`: `None` when absent or not a DSM code (the
/// cached value stays), `Some("")` for "", "def" or a code without an SDK
/// locale (follow the browser), else the code.
fn language_hint(params: &str) -> Option<String> {
    #[derive(serde::Deserialize)]
    struct Hint {
        #[serde(rename = "dsmLanguage")]
        dsm_language: Option<String>,
    }
    let value = serde_json::from_str::<Hint>(params).ok()?.dsm_language?;
    if !value.is_empty() && !(value.len() == 3 && value.bytes().all(|b| b.is_ascii_lowercase())) {
        return None;
    }
    if DSM_LOCALES.iter().any(|(code, _)| *code == value) {
        Some(value)
    } else {
        Some(String::new())
    }
}

/// A DSM code becomes `(code, locale)`, a valid BCP 47 tag `("", tag)`, and
/// anything else `("", "")`.
fn normalize(lang: &str) -> (String, String) {
    if let Some((code, locale)) = DSM_LOCALES.iter().find(|(code, _)| *code == lang) {
        return ((*code).into(), (*locale).into());
    }
    if lang.len() <= 35 && language_tag(lang) {
        return (String::new(), lang.into());
    }
    (String::new(), String::new())
}

/// `^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,3}$`, as the Go SDK checks it.
fn language_tag(tag: &str) -> bool {
    let mut parts = tag.split('-');
    let primary = parts.next().unwrap_or("");
    if !(2..=8).contains(&primary.len()) || !primary.bytes().all(|b| b.is_ascii_alphabetic()) {
        return false;
    }
    let mut subtags = 0;
    for part in parts {
        subtags += 1;
        if subtags > 3
            || !(1..=8).contains(&part.len())
            || !part.bytes().all(|b| b.is_ascii_alphanumeric())
        {
            return false;
        }
    }
    true
}
