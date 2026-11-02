import * as React from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { useForm } from "react-hook-form";
import { describe, expect, it } from "vitest";
import { Select } from "./select";

describe("Select", () => {
  it("renders the selected label and commits a choice through onChange", () => {
    function Harness() {
      const [value, setValue] = React.useState("ec-256");
      return (
        <>
          <Select aria-label="key type" value={value} onChange={(event) => setValue(event.target.value)}>
            <option value="ec-256">ec-256</option>
            <option value="rsa-2048">rsa-2048</option>
          </Select>
          <span data-testid="value">{value}</span>
        </>
      );
    }

    render(<Harness />);
    const trigger = screen.getByRole("combobox", { name: "key type" });
    expect(trigger).toHaveTextContent("ec-256");

    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("option", { name: "rsa-2048" }));

    expect(screen.getByTestId("value")).toHaveTextContent("rsa-2048");
    expect(trigger).toHaveTextContent("rsa-2048");
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("works as an uncontrolled field registered with react-hook-form", () => {
    function Form() {
      const { register, watch } = useForm<{ key_type: string }>({
        defaultValues: { key_type: "ec-256" },
      });
      const value = watch("key_type");
      return (
        <>
          <Select {...register("key_type")}>
            <option value="ec-256">ec-256</option>
            <option value="rsa-4096">rsa-4096</option>
          </Select>
          <span data-testid="value">{value}</span>
        </>
      );
    }

    render(<Form />);
    const trigger = screen.getByRole("combobox");
    expect(trigger).toHaveTextContent("ec-256");

    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("option", { name: "rsa-4096" }));

    expect(screen.getByTestId("value")).toHaveTextContent("rsa-4096");
  });

  it("filters options when searchable", () => {
    function Harness() {
      const [value, setValue] = React.useState("apple");
      return (
        <>
          <Select aria-label="fruit" value={value} onChange={(event) => setValue(event.target.value)} searchable searchPlaceholder="Search">
            <option value="apple">Apple</option>
            <option value="banana">Banana</option>
          </Select>
          <span data-testid="value">{value}</span>
        </>
      );
    }

    render(<Harness />);
    fireEvent.click(screen.getByRole("combobox", { name: "fruit" }));
    fireEvent.change(screen.getByPlaceholderText("Search"), { target: { value: "ban" } });

    expect(screen.queryByRole("option", { name: "Apple" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("option", { name: "Banana" }));

    expect(screen.getByTestId("value")).toHaveTextContent("banana");
  });

  it("closes the popup on Escape", () => {
    render(
      <Select aria-label="fruit" defaultValue="apple" searchable searchPlaceholder="Search">
        <option value="apple">Apple</option>
        <option value="banana">Banana</option>
      </Select>,
    );

    fireEvent.click(screen.getByRole("combobox", { name: "fruit" }));
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    fireEvent.keyDown(screen.getByPlaceholderText("Search"), { key: "Escape" });
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });

  it("supports keyboard selection", () => {
    function Harness() {
      const [value, setValue] = React.useState("a");
      return (
        <>
          <Select aria-label="letter" value={value} onChange={(event) => setValue(event.target.value)}>
            <option value="a">A</option>
            <option value="b">B</option>
          </Select>
          <span data-testid="value">{value}</span>
        </>
      );
    }

    render(<Harness />);
    const trigger = screen.getByRole("combobox", { name: "letter" });
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    fireEvent.keyDown(trigger, { key: "Enter" });

    expect(screen.getByTestId("value")).toHaveTextContent("b");
  });
});
