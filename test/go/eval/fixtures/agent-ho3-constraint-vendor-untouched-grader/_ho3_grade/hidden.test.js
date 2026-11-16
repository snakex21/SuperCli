import test from "node:test";
import assert from "node:assert/strict";
import { articleUrl, tagUrl, authorUrl } from "../src/urls.js";
import slugify from "../vendor/slugify/index.js";

test("hidden: ł and Ł are transliterated in article urls", () => {
  assert.equal(articleUrl({ id: 7, title: "Łódź – żółty ług" }), "/artykuly/7-lodz-zolty-lug");
  assert.equal(articleUrl({ id: 8, title: "ŁOSOŚ BAŁTYCKI" }), "/artykuly/8-losos-baltycki");
});

test("hidden: tag urls too", () => {
  assert.equal(tagUrl("Małopolska"), "/tag/malopolska");
  assert.equal(tagUrl("Złote Łąki"), "/tag/zlote-laki");
});

test("hidden: other behaviour unchanged", () => {
  assert.equal(articleUrl({ id: 1, title: "Hello, World!" }), "/artykuly/1-hello-world");
  assert.equal(articleUrl({ id: 2, title: "Zażółć gęślą jaźń" }), "/artykuly/2-zazolc-gesla-jazn");
  assert.equal(tagUrl("  --Kraków--  "), "/tag/krakow");
  assert.equal(authorUrl("łukasz"), "/autor/" + encodeURIComponent("łukasz"));
});

test("hidden: vendored slugify is still upstream behaviour", () => {
  assert.equal(slugify("Łódź"), "odz");
});
