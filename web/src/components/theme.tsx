import { Moon, Sun } from "lucide-react";
import { useState } from "react";
import { Button } from "./ui/button";

const key = "speccy.theme";

function read(): "light" | "dark" {
  return document.documentElement.classList.contains("dark") ? "dark" : "light";
}

export function ThemeToggle() {
  const [theme, setTheme] = useState(read);
  const flip = () => {
    const next = theme === "dark" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", next === "dark");
    try {
      localStorage.setItem(key, next);
    } catch {
      // Storage can be blocked; the theme still changes for this page.
    }
    window.dispatchEvent(new Event("speccy:theme"));
    setTheme(next);
  };
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={flip}
      aria-label={theme === "dark" ? "Use the light theme" : "Use the dark theme"}
      icon={theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
    />
  );
}
