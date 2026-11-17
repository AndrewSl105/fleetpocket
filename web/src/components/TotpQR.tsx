import { useEffect, useState } from 'react'
import QRCode from 'qrcode'

/** An otpauth:// URI as a QR code for authenticator apps. */
export function TotpQR({ uri, alt }: { uri: string; alt: string }) {
  const [src, setSrc] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    void QRCode.toDataURL(uri, { margin: 1, width: 200 }).then((url) => live && setSrc(url))
    return () => {
      live = false
    }
  }, [uri])
  return src ? <img src={src} alt={alt} width={200} height={200} className="mx-auto bg-white" /> : null
}
