export interface SnippetState {
  /**
   * Current snippet ID.
   */
  id?: string | null

  /**
   * Represents whether snippet is loading.
   */
  loading?: boolean

  /**
   * Contains snippet loading error.
   */
  error?: string | null
}

/**
 * Represents current workspace state.
 */
export interface WorkspaceState {
  /**
   * Represents current snippet state.
   *
   * Empty if snippet is not loaded.
   */
  snippet?: SnippetState | null

  /**
   * Current selected file name.
   */
  selectedFile?: string | null

  /**
   * Key-value pair of file names and their content.
   */
  files?: {[filename: string]: string}
}
