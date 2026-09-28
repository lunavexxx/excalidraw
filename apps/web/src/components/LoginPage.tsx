import { TextField } from "@excalidraw/excalidraw/components/TextField";
import { languages, setLanguage, t } from "@excalidraw/excalidraw/i18n";
import { useEffect, useState } from "react";

import { getPreferredLanguage } from "../app-language/language-detector";
import { ApiError, login, register } from "../auth/api";

import BlobBottomCenter from "./login-assets/blob-bottom-center.svg?react";
import BlobTopLeft from "./login-assets/blob-top-left.svg?react";
import BlobTopRight from "./login-assets/blob-top-right.svg?react";

import "./LoginPage.scss";

const PHONE_RE = /^1[3-9]\d{9}$/;

export const normalizePhoneInput = (input: string) =>
  input.replace(/\D/g, "").replace(/^86(?=1[3-9]\d{9}$)/, "");

export type LoginMode = "login" | "signup";

const FieldError = ({ message }: { message?: string }) =>
  message ? <div className="login-page__field-error">{message}</div> : null;

export const LoginPage = ({ initialMode }: { initialMode: LoginMode }) => {
  const [mode, setMode] = useState<LoginMode>(initialMode);

  const [phone, setPhone] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [nickname, setNickname] = useState("");
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState("");
  const [loading, setLoading] = useState(false);

  const isSignUp = mode === "signup";

  const switchMode = (next: LoginMode) => {
    setMode(next);
    setFieldErrors({});
    setFormError("");
    // 保持 URL 与模式一致(/login ↔ /signup),不触发整页刷新
    window.history.pushState(null, "", `/${next}`);
  };

  const validate = (): string | null => {
    const digits = normalizePhoneInput(phone);
    if (!PHONE_RE.test(digits)) {
      return t("loginPage.invalidPhone");
    }
    if (password.length < 8) {
      return t("loginPage.passwordTooShort");
    }
    if (isSignUp && password !== confirmPassword) {
      return t("loginPage.passwordMismatch");
    }
    return null;
  };

  const handleSubmit = async (event: React.FormEvent) => {
    event.preventDefault();
    setFormError("");
    const validationError = validate();
    if (validationError) {
      setFormError(validationError);
      return;
    }
    setLoading(true);
    try {
      isSignUp
        ? await register(
            normalizePhoneInput(phone),
            password,
            nickname.trim() || undefined,
          )
        : await login(normalizePhoneInput(phone), password);
      // 编辑器装载时经静默 refresh 恢复会话
      window.location.assign("/");
    } catch (error: any) {
      if (error instanceof ApiError) {
        switch (error.code) {
          case 40900: // phone_taken
            setFieldErrors({ phone: t("loginPage.phoneTaken") });
            break;
          case 40101: // invalid_credentials
            setFormError(t("loginPage.invalidCredentials"));
            break;
          case 40000: // invalid_request
            setFormError(error.message);
            break;
          default:
            setFormError(t("loginPage.tryAgainLater"));
        }
      } else {
        setFormError(t("loginPage.tryAgainLater"));
      }
      setLoading(false);
    }
  };

  return (
    <div className="login-page">
      <BlobTopLeft className="login-page__blob login-page__blob--top-left" />
      <BlobTopRight className="login-page__blob login-page__blob--top-right" />
      <BlobBottomCenter className="login-page__blob login-page__blob--bottom-center" />

      <form className="login-page__card" onSubmit={handleSubmit}>
        <div className="login-page__eyebrow">
          {isSignUp ? t("loginPage.getStarted") : t("loginPage.welcomeBack")}
        </div>
        <h2 className="login-page__title">
          {isSignUp ? t("loginPage.signUpTitle") : t("loginPage.signInTitle")}
        </h2>
        <p className="login-page__subtitle">
          {isSignUp
            ? t("loginPage.signUpSubtitle")
            : t("loginPage.signInSubtitle")}
        </p>

        <div className="login-page__field">
          <TextField
            value={phone}
            onChange={(value) => {
              setPhone(value);
              setFieldErrors((prev) => ({ ...prev, phone: "" }));
            }}
            label={t("loginPage.phone")}
            placeholder={t("loginPage.phonePlaceholder")}
            fullWidth
          />
          <FieldError message={fieldErrors.phone} />
        </div>

        <div className="login-page__field">
          <TextField
            value={password}
            onChange={setPassword}
            label={t("loginPage.password")}
            placeholder={t("loginPage.passwordPlaceholder")}
            isRedacted
            fullWidth
          />
        </div>

        {isSignUp && (
          <>
            <div className="login-page__field">
              <TextField
                value={confirmPassword}
                onChange={setConfirmPassword}
                label={t("loginPage.confirmPassword")}
                isRedacted
                fullWidth
              />
            </div>
            <div className="login-page__field">
              <TextField
                value={nickname}
                onChange={setNickname}
                label={t("loginPage.nickname")}
                placeholder={t("loginPage.nicknamePlaceholder")}
                fullWidth
              />
            </div>
          </>
        )}

        {formError && (
          <div className="login-page__form-error" role="alert">
            {formError}
          </div>
        )}

        <button type="submit" className="login-page__submit" disabled={loading}>
          {loading
            ? t("loginPage.signingIn")
            : isSignUp
            ? t("loginPage.signUp")
            : t("loginPage.signIn")}
        </button>

        <div className="login-page__switch">
          {isSignUp ? (
            <>
              {t("loginPage.hasAccount")}{" "}
              <button type="button" onClick={() => switchMode("login")}>
                {t("loginPage.switchToSignIn")}
              </button>
            </>
          ) : (
            <>
              {t("loginPage.noAccount")}{" "}
              <button type="button" onClick={() => switchMode("signup")}>
                {t("loginPage.switchToSignUp")}
              </button>
            </>
          )}
        </div>

        <div className="login-page__divider">
          <span>{t("loginPage.orContinueWith")}</span>
        </div>

        <div className="login-page__alt-methods">
          <button type="button" className="login-page__alt" disabled>
            {t("loginPage.wechatLogin")}
            <span className="login-page__alt-badge">
              {t("loginPage.comingSoon")}
            </span>
          </button>
          <button type="button" className="login-page__alt" disabled>
            {t("loginPage.smsLogin")}
            <span className="login-page__alt-badge">
              {t("loginPage.comingSoon")}
            </span>
          </button>
        </div>

        <div className="login-page__footer">
          <span className="login-page__forgot">
            {t("loginPage.forgotPassword")}
          </span>
        </div>
      </form>
    </div>
  );
};

/**
 * 独立登录路由(/login /signup)的根组件:不带编辑器,只挂登录页所需
 * 的最小上下文(全局样式来自 @excalidraw/excalidraw 入口、语言经
 * setLanguage、主题读编辑器同款 localStorage 键)。
 */
export const LoginApp = () => {
  const [mode, setMode] = useState<LoginMode>(() =>
    window.location.pathname.endsWith("/signup") ? "signup" : "login",
  );

  useEffect(() => {
    const onPopState = () =>
      setMode(
        window.location.pathname.endsWith("/signup") ? "signup" : "login",
      );
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  // 与编辑器共享语言选择(App.tsx 的 useAppLangCode 同源)。
  // t 是模块级函数、不触发重渲染,因此等 locale 加载完再渲染页面。
  // 不能按 getLanguage() 跳过:setLanguage 会先同步改 currentLang、
  // 数据异步加载,StrictMode 双跑 effect 时会拿空数据渲染成英文。
  const [langReady, setLangReady] = useState(false);

  useEffect(() => {
    const code = getPreferredLanguage();
    const lang = languages.find((l) => l.code === code) ?? languages[0];
    void setLanguage(lang).then(() => setLangReady(true));
  }, []);

  const storedTheme = (() => {
    try {
      return localStorage.getItem("excalidraw-theme");
    } catch {
      return null;
    }
  })();
  const prefersDark =
    typeof window.matchMedia === "function" &&
    window.matchMedia("(prefers-color-scheme: dark)").matches;
  const isDark = storedTheme ? storedTheme === "dark" : prefersDark;

  if (!langReady) {
    return null;
  }

  return (
    <div
      className={`excalidraw${isDark ? " theme--dark" : ""}`}
      style={{ height: "100%", display: "flex", flexDirection: "column" }}
    >
      <LoginPage initialMode={mode} />
    </div>
  );
};
