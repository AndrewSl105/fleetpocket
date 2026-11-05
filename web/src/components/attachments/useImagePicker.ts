import { useCallback, useEffect, useRef, useState } from 'react'
import { addFiles, filesFrom, type Picked } from '@/lib/attachments'

/**
 * Images and other files picked for a task or message: from the file inputs
 * (camera or gallery on phones), pasted from the clipboard or dropped on the
 * form. Owns the image preview object URLs and revokes them when one leaves.
 */
export function useImagePicker() {
  // The list lives in a ref that only event handlers change (they also make
  // and revoke the preview URLs); the state copy re-renders.
  const list = useRef<Picked[]>([])
  const [picked, setPicked] = useState<Picked[]>([])
  const [errors, setErrors] = useState<string[]>([])
  const [dragging, setDragging] = useState(false)

  useEffect(() => {
    const ref = list
    return () => ref.current.forEach(revoke)
  }, [])

  const set = useCallback((next: Picked[], errs: string[]) => {
    list.current = next
    setPicked(next)
    setErrors(errs)
  }, [])

  const add = useCallback(
    (files: File[]) => {
      if (files.length === 0) return
      const { next, errors } = addFiles(list.current, files, (f) => URL.createObjectURL(f))
      set(next, errors)
    },
    [set],
  )

  const remove = useCallback(
    (id: string) => {
      const gone = list.current.find((p) => p.id === id)
      if (gone) revoke(gone)
      set(
        list.current.filter((p) => p.id !== id),
        [],
      )
    },
    [set],
  )

  const clear = useCallback(() => {
    list.current.forEach(revoke)
    set([], [])
  }, [set])

  /** Props of a file input; each attach button renders its own. */
  const inputProps = {
    type: 'file' as const,
    multiple: true,
    hidden: true,
    onChange: (e: React.ChangeEvent<HTMLInputElement>) => {
      add(Array.from(e.target.files ?? []))
      // Picking the same file again must fire change again.
      e.target.value = ''
    },
  }

  /** Paste handler for a text field: takes files, leaves text pastes alone. */
  const onPaste = (e: React.ClipboardEvent) => {
    const files = filesFrom(e.clipboardData)
    if (files.length === 0) return
    e.preventDefault()
    add(files)
  }

  /** Drag-and-drop on a container (desktop). */
  const dropProps = {
    onDragOver: (e: React.DragEvent) => {
      if (!Array.from(e.dataTransfer.types).includes('Files')) return
      e.preventDefault()
      setDragging(true)
    },
    onDragLeave: (e: React.DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDragging(false)
    },
    onDrop: (e: React.DragEvent) => {
      if (!Array.from(e.dataTransfer.types).includes('Files')) return
      e.preventDefault()
      setDragging(false)
      add(filesFrom(e.dataTransfer))
    },
  }

  return {
    picked,
    files: picked.map((p) => p.file),
    errors,
    dragging,
    add,
    remove,
    clear,
    inputProps,
    onPaste,
    dropProps,
  }
}

function revoke(p: Picked) {
  if (p.url) URL.revokeObjectURL(p.url)
}

export type ImagePicker = ReturnType<typeof useImagePicker>
