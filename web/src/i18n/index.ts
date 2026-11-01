import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import zh from "./zh";
import en from "./en";

const stored = localStorage.getItem("kpcc_lang");
const browser = navigator.language.toLowerCase().startsWith("zh") ? "zh-CN" : "en";

void i18n.use(initReactI18next).init({
  resources: { "zh-CN": zh, en },
  lng: stored || browser,
  fallbackLng: "zh-CN",
  interpolation: { escapeValue: false },
});

export default i18n;
