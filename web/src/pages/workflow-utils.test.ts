import { describe, expect, it } from "vitest";
import type { Workflow } from "@/api/types";
import {
  type EditForm,
  defaultConfig,
  emptyForm,
  fromWorkflow,
  newNode,
  stepsToGraph,
  upstreamCertificateID,
  validateEditForm,
} from "./workflow-utils";

function validForm(): EditForm {
  const apply = newNode("apply", { x: 80, y: 80 });
  apply.config = { ...defaultConfig("apply"), certificate_id: "cert-1" };
  const deploy = newNode("deploy", { x: 340, y: 80 });
  const notify = newNode("notify", { x: 600, y: 80 });
  notify.config = { ...notify.config, title: "done" };
  return {
    name: "workflow",
    description: "",
    enabled: true,
    trigger_type: "manual",
    schedule: "daily 03:00",
    nodes: [apply, deploy, notify],
    edges: [
      { id: "e1", from: apply.id, to: deploy.id, condition: "success" },
      { id: "e2", from: deploy.id, to: notify.id, condition: "success" },
    ],
    allow_multiple_starts: false,
  };
}

describe("workflow editor utils", () => {
  it("ships usable apply defaults instead of only rendering them", () => {
    const cfg = defaultConfig("apply");
    expect(cfg.key_type).toBe("ec-256");
    expect(cfg.challenge_type).toBe("dns-01");
    expect(cfg.ca_account_id).toBe("");
  });

  it("accepts a connected apply-deploy-notify workflow", () => {
    expect(validateEditForm(validForm())).toEqual([]);
  });

  it("does not report the default notify node as invalid", () => {
    const keys = validateEditForm(emptyForm()).map((issue) => issue.key);
    expect(keys).toContain("workflow.validation.apply");
    expect(keys).not.toContain("workflow.validation.notify");
  });

  it("rejects multiple start nodes unless explicitly allowed", () => {
    const form = validForm();
    form.nodes.push(newNode("wait", { x: 80, y: 300 }));
    expect(validateEditForm(form).some((issue) => issue.key === "workflow.validation.multipleStarts")).toBe(true);
    form.allow_multiple_starts = true;
    expect(validateEditForm(form)).toEqual([]);
  });

  it("rejects cycles and duplicate edges", () => {
    const form = validForm();
    form.edges.push({ id: "e3", from: form.nodes[2].id, to: form.nodes[0].id, condition: "success" });
    expect(validateEditForm(form).some((issue) => issue.key === "workflow.validation.cycle")).toBe(true);

    const duplicate = validForm();
    duplicate.edges.push({ id: "e3", from: duplicate.edges[0].from, to: duplicate.edges[0].to, condition: "always" });
    expect(validateEditForm(duplicate).some((issue) => issue.key === "workflow.validation.duplicateEdge")).toBe(true);
  });

  it("resolves the certificate inherited by a deploy node", () => {
    const form = validForm();
    const [apply, deploy, notify] = form.nodes;
    expect(upstreamCertificateID(deploy.id, form.nodes, form.edges)).toBe("cert-1");

    // Transparent intermediate nodes do not break the lookup.
    form.edges = [
      { id: "e1", from: apply.id, to: notify.id, condition: "success" },
      { id: "e2", from: notify.id, to: deploy.id, condition: "success" },
    ];
    expect(upstreamCertificateID(deploy.id, form.nodes, form.edges)).toBe("cert-1");

    // An inline apply has no certificate id until run time.
    apply.config = { ...apply.config, certificate_id: "" };
    expect(upstreamCertificateID(deploy.id, form.nodes, form.edges)).toBeUndefined();

    // Two different certificates at the same distance are ambiguous.
    apply.config = { ...apply.config, certificate_id: "cert-1" };
    const other = newNode("apply", { x: 80, y: 260 });
    other.config = { ...defaultConfig("apply"), certificate_id: "cert-2" };
    form.nodes.push(other);
    form.edges = [
      { id: "e1", from: apply.id, to: deploy.id, condition: "success" },
      { id: "e2", from: other.id, to: deploy.id, condition: "success" },
    ];
    expect(upstreamCertificateID(deploy.id, form.nodes, form.edges)).toBeUndefined();
  });

  it("converts legacy ordered steps into a connected graph", () => {
    const workflow: Workflow = {
      id: "w1",
      name: "legacy",
      description: "",
      enabled: true,
      trigger_type: "manual",
      schedule: "",
      created_at: 0,
      updated_at: 0,
      definition: {
        version: 1,
        steps: [
          { id: "a", type: "wait", config: { seconds: 1 } },
          { id: "b", type: "notify", config: { title: "done" } },
        ],
      },
    };
    const form = fromWorkflow(workflow);
    expect(form.nodes.map((n) => n.id)).toEqual(["a", "b"]);
    expect(form.edges).toEqual([{ id: "edge-1", from: "a", to: "b", condition: "success" }]);
    expect(form.allow_multiple_starts).toBe(false);
  });

  it("preserves a legacy parallel first group by opting into multiple starts", () => {
    const workflow: Workflow = {
      id: "w2",
      name: "legacy parallel",
      description: "",
      enabled: true,
      trigger_type: "manual",
      schedule: "",
      created_at: 0,
      updated_at: 0,
      definition: {
        version: 1,
        steps: [
          { id: "a", type: "wait", config: { seconds: 1 }, parallel_group: "g" },
          { id: "b", type: "wait", config: { seconds: 1 }, parallel_group: "g" },
        ],
      },
    };
    const form = fromWorkflow(workflow);
    expect(form.allow_multiple_starts).toBe(true);
    expect(validateEditForm({ ...form, name: "parallel" })).toEqual([]);
  });

  it("keeps parallel groups connected from every previous node", () => {
    const graph = stepsToGraph([
      { id: "a", type: "wait", config: { seconds: 1 } },
      { id: "b", type: "notify", config: { title: "b" }, parallel_group: "g" },
      { id: "c", type: "notify", config: { title: "c" }, parallel_group: "g" },
      { id: "d", type: "wait", config: { seconds: 1 } },
    ]);
    expect(graph.nodes.map((n) => n.id)).toEqual(["a", "b", "c", "d"]);
    expect(graph.edges).toEqual([
      { id: "edge-1", from: "a", to: "b", condition: "success" },
      { id: "edge-2", from: "a", to: "c", condition: "success" },
      { id: "edge-3", from: "b", to: "d", condition: "success" },
      { id: "edge-4", from: "c", to: "d", condition: "success" },
    ]);
  });
});
