// Language (Italian for Italian browsers, else English; remembered), and
// the download links of the newest stable release, read from GitHub (betas
// are offered only to who opts in, never from the site).
"use strict";
(() => {
  const root = document.documentElement;
  let lang = "en";
  try { lang = localStorage.getItem("lang") || ((navigator.language || "").startsWith("it") ? "it" : "en"); } catch { /* private mode */ }
  const set = (l) => { root.lang = l; document.getElementById("lang").textContent = l === "it" ? "EN" : "IT"; };
  set(lang);
  document.getElementById("lang").onclick = () => {
    const l = root.lang === "it" ? "en" : "it";
    try { localStorage.setItem("lang", l); } catch { /* private mode */ }
    set(l);
  };
  // releases/latest skips drafts and pre-releases.
  fetch("https://api.github.com/repos/silirim/VodMax/releases/latest")
    .then((r) => (r.ok ? r.json() : null))
    .then((rel) => {
      if (!rel || !Array.isArray(rel.assets)) return;
      for (const v of document.querySelectorAll(".ver")) v.textContent = rel.tag_name;
      for (const a of document.querySelectorAll("[data-asset]")) {
        const asset = rel.assets.find((x) => x.name.endsWith(a.dataset.asset));
        if (asset) a.href = asset.browser_download_url;
      }
    })
    .catch(() => {});
})();
