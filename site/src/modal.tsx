import { useEffect, useRef, type MouseEvent, type ReactNode } from 'react'

export function Modal({
  open,
  title,
  titleId,
  onClose,
  children,
}: {
  open: boolean
  title: string
  titleId: string
  onClose: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open) {
      if (!dialog.open) dialog.showModal()
      dialog.querySelector<HTMLElement>('input')?.focus()
      return
    }
    if (dialog.open) dialog.close()
  }, [open])

  function backdrop(event: MouseEvent<HTMLDialogElement>) {
    if (event.target === event.currentTarget) onClose()
  }

  return (
    <dialog ref={ref} className="modal" aria-labelledby={titleId} onClose={onClose} onClick={backdrop}>
      <div className="modal-card">
        <div className="modal-head">
          <h2 id={titleId}>{title}</h2>
          <button type="button" className="modal-close" aria-label="Close" onClick={onClose}>
            <CloseIcon />
          </button>
        </div>
        {children}
      </div>
    </dialog>
  )
}

function CloseIcon() {
  return (
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path d="M4 4 12 12M12 4 4 12" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}
