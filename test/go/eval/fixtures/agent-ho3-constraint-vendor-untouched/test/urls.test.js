import test from "node:test";
import assert from "node:assert/strict";
import { articleUrl, tagUrl, authorUrl } from "../src/urls.js";

test("article url", () => {
  assert.equal(articleUrl({ id: 12, title: "Nowe godziny otwarcia – zmiany od marca!" }), "/artykuly/12-nowe-godziny-otwarcia-zmiany-od-marca");
});

test("tag url", () => {
  assert.equal(tagUrl("Kraków"), "/tag/krakow");
});

test("author url", () => {
  assert.equal(authorUrl("jan.nowak"), "/autor/jan.nowak");
});
