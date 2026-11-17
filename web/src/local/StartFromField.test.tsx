import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { StartFromField } from './StartFromField'

describe('StartFromField', () => {
  it('switches between the last commit and the uncommitted changes', async () => {
    const onChange = vi.fn()
    const { rerender } = render(<StartFromField value="last_commit" onChange={onChange} />)
    expect(screen.getByRole('radio', { name: 'Last commit' })).toBeChecked()
    expect(screen.queryByText(/WIP/)).toBeNull()
    await userEvent.click(screen.getByRole('radio', { name: /Include my uncommitted changes/ }))
    expect(onChange).toHaveBeenCalledWith('working_tree')
    rerender(<StartFromField value="working_tree" onChange={onChange} dirty />)
    expect(screen.getByRole('radio', { name: /Include my uncommitted changes/ })).toBeChecked()
    expect(screen.getByText(/copied as a "WIP" commit/)).toBeInTheDocument()
    expect(screen.getByText('uncommitted changes')).toBeInTheDocument()
  })
})
