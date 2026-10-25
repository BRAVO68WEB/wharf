import { useState } from 'react'

export function CopyButton({ value, label = 'Copy' }: { value: string; label?: string }) {
  const [text, setText] = useState(label)
  return (
    <button
      type="button"
      className="copy"
      onClick={() => {
        void navigator.clipboard.writeText(value).then(
          () => {
            setText('Copied')
            window.setTimeout(() => setText(label), 1200)
          },
          () => setText('Copy failed'),
        )
      }}
    >
      <CopyIcon />
      {text}
    </button>
  )
}

export function Chevron({ open }: { open: boolean }) {
  return (
    <svg className={open ? 'chev open' : 'chev'} viewBox="0 0 16 16" aria-hidden="true">
      <path d="M6 3.5 11 8 6 12.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

function CopyIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <rect x="5.5" y="5.5" width="8" height="8" rx="1" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <path d="M10.5 5.5V3.2A1.2 1.2 0 0 0 9.3 2H3.2A1.2 1.2 0 0 0 2 3.2v6.1a1.2 1.2 0 0 0 1.2 1.2H5.5" fill="none" stroke="currentColor" strokeWidth="1.4" />
    </svg>
  )
}
