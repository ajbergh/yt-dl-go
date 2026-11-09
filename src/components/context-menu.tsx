import { useEffect, useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactElement } from "react";
import { createPortal } from "react-dom";

export type ContextMenuItem = {
  label: string;
  action: () => void | Promise<void>;
  disabled?: boolean;
  danger?: boolean;
};

// Attach to the surface itself so grid placement and sibling spacing stay intact.
// Editable fields and native media controls keep their browser/WebView menus.
export function ContextMenuArea({ label, items, children, onOpen }: {
  label: string;
  items: ContextMenuItem[];
  children: ReactElement<HTMLAttributes<HTMLElement>>;
  onOpen?: () => void;
}) {
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
  const trigger = useRef<HTMLElement | null>(null);
  const menu = useRef<HTMLDivElement>(null);
  function close(restoreFocus = false) {
    setPosition(null);
    if (restoreFocus && trigger.current?.isConnected) trigger.current.focus({ preventScroll: true });
  }
  function open(target: EventTarget, x: number, y: number) {
    const element = target as HTMLElement;
    trigger.current = element.closest<HTMLElement>('button, [tabindex], a[href]') || element;
    onOpen?.();
    setPosition({ x, y });
  }
  useLayoutEffect(() => {
    if (!position || !menu.current) return;
    const element = menu.current;
    const bounds = element.getBoundingClientRect();
    element.style.left = `${Math.max(8, Math.min(position.x, window.innerWidth - bounds.width - 8))}px`;
    element.style.top = `${Math.max(8, Math.min(position.y, window.innerHeight - bounds.height - 8))}px`;
    (element.querySelector<HTMLElement>('button:not(:disabled)') || element).focus({ preventScroll: true });
  }, [position]);
  useEffect(() => {
    if (!position) return;
    function outside(event: Event) {
      if (!menu.current?.contains(event.target as Node)) setPosition(null);
    }
    function dismiss() { setPosition(null); }
    function scroll(event: Event) {
      if (!menu.current?.contains(event.target as Node)) dismiss();
    }
    window.addEventListener("pointerdown", outside, true);
    window.addEventListener("contextmenu", outside, true);
    window.addEventListener("scroll", scroll, true);
    window.addEventListener("resize", dismiss);
    window.addEventListener("blur", dismiss);
    return () => {
      window.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("contextmenu", outside, true);
      window.removeEventListener("scroll", scroll, true);
      window.removeEventListener("resize", dismiss);
      window.removeEventListener("blur", dismiss);
    };
  }, [position]);
  const nativeTarget = (target: EventTarget) => (target as HTMLElement).closest('input, textarea, select, [contenteditable]:not([contenteditable="false"]), video, audio');
  const Surface = children.type;
  return <>
    <Surface {...children.props} onContextMenu={(event: React.MouseEvent<HTMLElement>) => {
    children.props.onContextMenu?.(event);
    if (event.defaultPrevented) return;
    if (nativeTarget(event.target) || !items.length) return;
    event.preventDefault();
    event.stopPropagation();
    const bounds = (event.target as HTMLElement).getBoundingClientRect();
    open(event.target, event.clientX || bounds.left, event.clientY || bounds.bottom);
  }} onKeyDown={(event: React.KeyboardEvent<HTMLElement>) => {
    children.props.onKeyDown?.(event);
    if (event.defaultPrevented) return;
    if (nativeTarget(event.target) || !items.length) return;
    if (event.key === "ContextMenu" || (event.shiftKey && event.key === "F10")) {
      event.preventDefault();
      event.stopPropagation();
      const bounds = (event.target as HTMLElement).getBoundingClientRect();
      open(event.target, bounds.left, bounds.bottom);
    }
  }} />
    {position && createPortal(<div ref={menu} role="menu" aria-label={label} tabIndex={-1}
      className="fixed z-[100] max-h-[calc(100vh-16px)] w-64 max-w-[calc(100vw-16px)] overflow-y-auto rounded-xl border border-neutral-700 bg-neutral-900 p-1.5 text-neutral-100 shadow-2xl"
      style={{ left: position.x, top: position.y }}
      onContextMenu={event => { event.preventDefault(); event.stopPropagation(); }}
      onKeyDown={event => {
        event.stopPropagation();
        if (event.key === "Escape" || event.key === "Tab") {
          event.preventDefault();
          close(true);
          return;
        }
        const buttons = [...(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') || [])];
        const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
        let next: HTMLButtonElement | undefined;
        if (event.key === "ArrowDown") next = buttons[(index + 1) % buttons.length];
        else if (event.key === "ArrowUp") next = buttons[(index - 1 + buttons.length) % buttons.length];
        else if (event.key === "Home") next = buttons[0];
        else if (event.key === "End") next = buttons.at(-1);
        else if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && event.key !== " ") {
          next = [...buttons.slice(index + 1), ...buttons.slice(0, index + 1)].find(button => button.textContent?.toLowerCase().startsWith(event.key.toLowerCase()));
        }
        if (next) { event.preventDefault(); next.focus(); }
      }} onClick={event => event.stopPropagation()}>
      {items.map(item => <button key={item.label} type="button" role="menuitem" disabled={item.disabled}
        className={`block w-full rounded-lg px-3 py-2 text-left text-xs focus:bg-neutral-800 focus:outline-none hover:bg-neutral-800 disabled:opacity-40 ${item.danger ? "text-red-300" : ""}`}
        onClick={() => { close(true); void item.action(); }}>{item.label}</button>)}
    </div>, document.body)}
  </>;
}
