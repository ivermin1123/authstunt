// Minimal SMTP client, hand-written on purpose: the suite needs exactly one
// happy dialogue (EHLO, MAIL FROM, RCPT TO, DATA, QUIT) against the real
// listener, and a dependency would be more surface than these lines.

import net from 'node:net'

export interface Mail {
  host: string
  port: number
  from: string
  to: string
  subject: string
  text: string
}

/** Sends one message and resolves after the server acks DATA with 250,
 * which under the server's ack contract means the mail is durably stored. */
export function sendMail(mail: Mail): Promise<void> {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ host: mail.host, port: mail.port })
    socket.setEncoding('utf8')
    const timer = setTimeout(() => {
      socket.destroy()
      reject(new Error('smtp dialogue did not finish in 15s'))
    }, 15_000)

    const payload = [
      `From: <${mail.from}>`,
      `To: <${mail.to}>`,
      `Subject: ${mail.subject}`,
      '',
      mail.text,
    ].join('\r\n').replace(/^\./gm, '..')

    // Each step waits for the final line of one reply (code + space), then
    // sends the next command. Multiline replies keep the hyphen form
    // (250-...) until the last line, so matching "NNN " is the terminator.
    const steps: { expect: string; send?: string }[] = [
      { expect: '220', send: `EHLO client.test\r\n` },
      { expect: '250', send: `MAIL FROM:<${mail.from}>\r\n` },
      { expect: '250', send: `RCPT TO:<${mail.to}>\r\n` },
      { expect: '250', send: `DATA\r\n` },
      { expect: '354', send: `${payload}\r\n.\r\n` },
      { expect: '250', send: `QUIT\r\n` },
      { expect: '221' },
    ]
    let step = 0
    let buffer = ''

    const fail = (reason: string): void => {
      clearTimeout(timer)
      socket.destroy()
      reject(new Error(reason))
    }

    socket.on('error', (err) => { fail(`smtp socket: ${err.message}`) })
    socket.on('data', (chunk: string) => {
      buffer += chunk
      for (;;) {
        const newline = buffer.indexOf('\r\n')
        if (newline < 0) {
          return
        }
        const line = buffer.slice(0, newline)
        buffer = buffer.slice(newline + 2)
        if (line.length >= 4 && line[3] === '-') {
          continue // one line of a multiline reply, not the last
        }
        const current = steps[step]
        if (current === undefined) {
          return
        }
        if (!line.startsWith(current.expect)) {
          fail(`smtp step ${String(step)}: expected ${current.expect}, got "${line}"`)
          return
        }
        if (current.send !== undefined) {
          socket.write(current.send)
        }
        step += 1
        if (step >= steps.length) {
          clearTimeout(timer)
          socket.end()
          resolve()
          return
        }
      }
    })
  })
}

/** A body whose only extractable OTP is the code given. The word "code"
 * next to the digits is what the extractor keys on. */
export function otpBody(code: string): string {
  return `Hello,\r\n\r\nYour verification code is ${code}.\r\n\r\nThe app`
}
